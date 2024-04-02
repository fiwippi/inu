package evaluate

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	g "cunicu.li/gont/v2/pkg"
	opt "cunicu.li/gont/v2/pkg/options"
	cptopt "cunicu.li/gont/v2/pkg/options/capture"
	cmdopt "cunicu.li/gont/v2/pkg/options/cmd"
	"github.com/florianl/go-tc"
	"github.com/florianl/go-tc/core"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// IPC Structs via Unix Domain Sockets

const (
	ReqNetem Request = iota
)

type Request int

const (
	AckNetem Ack = iota
)

type Ack int

// Hosts

func newHost(name string, alloc *IPv4Allocator, n *g.Network, sw *g.Switch) (*g.Host, string) {
	ip := <-alloc.NewIP
	h, err := n.AddHost(name,
		g.NewInterface("veth0", sw,
			opt.AddressIP(fmt.Sprintf("%s/%d", ip, alloc.PrefixLen()))),
	)
	if err != nil {
		panic(err)
	}
	return h, ip
}

func applyNetem(h *g.Host, config NetemConfig) {
	tcnl, err := tc.Open(&tc.Config{NetNS: int(h.NsHandle)})
	if err != nil {
		panic(err)
	}
	defer tcnl.Close()

	attr := h.Interface("veth0").Link.Attrs()
	qdisc := tc.Object{
		Msg: tc.Msg{
			Family:  unix.AF_INET,
			Ifindex: uint32(attr.Index),
			Handle:  core.BuildHandle(0x1, 0x0), // Name used to refer to the qdisc
			Parent:  tc.HandleRoot,
		},
		Attribute: config.Attribute(),
	}
	if err := tcnl.Qdisc().Add(&qdisc); err != nil {
		panic(err)
	}
}

// Netem

var GigabitNetem = NetemConfig{
	Latency: 5 * time.Millisecond,
	Jitter:  1 * time.Millisecond,
	Rate:    1e9,
	Loss:    0.01,
}

var WifiNetem = NetemConfig{
	Latency: 40 * time.Millisecond,
	Jitter:  4 * time.Millisecond,
	Rate:    4e7, // 5 MB = 4e7 bits
	Loss:    0.2,
}

type NetemConfig struct {
	Latency time.Duration
	Jitter  time.Duration
	Rate    uint64
	Loss    float64
}

func (c NetemConfig) Attribute() tc.Attribute {
	// go-tc multiplies the rate by 8, so we divide it beforehand,
	// e.g. if we specify it as 8, go-tc inputs it as 64
	r := c.Rate / 8

	return tc.Attribute{
		Kind: "netem",
		Netem: &tc.Netem{
			Qopt: tc.NetemQopt{
				Limit:   12500,
				Latency: durToTick(c.Latency),
				Loss:    uint32(c.Loss * float64(42949673)), // 42949673 = 100%
				Jitter:  durToTick(c.Jitter),
			},
			Rate64: &r,
		},
	}
}

func durToTick(d time.Duration) uint32 {
	t, err := core.Duration2TcTime(d)
	if err != nil {
		panic(err)
	}
	return core.Time2Tick(t)
}

// File Allocator

type FileAllocator struct {
	NewFile    <-chan string
	ReturnFile chan<- string
}

func AllocateFile(ctx context.Context, pattern string) *FileAllocator {
	mu := sync.Mutex{}
	fileMap := make(map[string]*os.File)
	newFile := make(chan string)
	returnFile := make(chan string)

	cleanup := func() {
		mu.Lock()
		defer mu.Lock()

		for _, f := range fileMap {
			if err := os.Remove(f.Name()); err != nil {
				panic(err)
			}
		}
	}

	go func() {
		for {
			f, err := os.CreateTemp(os.TempDir(), pattern)
			if err != nil {
				panic(err)
			}
			if err := f.Close(); err != nil {
				panic(err)
			}

			mu.Lock()
			fileMap[f.Name()] = f
			mu.Unlock()

			select {
			case newFile <- f.Name():
			case <-ctx.Done():
				cleanup()
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case name := <-returnFile:
				mu.Lock()
				f, ok := fileMap[name]
				if !ok {
					panic("cannot locate temp file")
				}
				delete(fileMap, name)
				mu.Unlock()

				if err := os.Remove(f.Name()); err != nil {
					panic(err)
				}
			case <-ctx.Done():
				cleanup()
				return
			}
		}
	}()

	return &FileAllocator{
		NewFile:    newFile,
		ReturnFile: returnFile,
	}
}

// IPv4 Allocator

type IPv4Allocator struct {
	NewIP    <-chan string
	ReturnIP chan<- string
	prefxLen uint
}

func (a *IPv4Allocator) PrefixLen() uint {
	return a.prefxLen
}

func AllocateIPv4(ctx context.Context, cidr string) *IPv4Allocator {
	// Calculate the CIDR range
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	r := make([]string, 0)
	for ip := ip.Mask(ipNet.Mask); ipNet.Contains(ip); incIP(ip) {
		r = append(r, ip.String())
	}
	r = r[1 : len(r)-1]

	// Setup and begin allocation
	mu := sync.Mutex{}
	newIP := make(chan string)
	returnIP := make(chan string)

	go func() {
		for {
			// This does not protect against cases where
			// we have no more IPs to allocate!
			mu.Lock()
			ip := r[0]
			r = r[1:]
			mu.Unlock()

			select {
			case newIP <- ip:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case ip := <-returnIP:
				mu.Lock()
				r = append(r, ip)
				mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	pLen, _ := ipNet.Mask.Size()
	return &IPv4Allocator{
		NewIP:    newIP,
		ReturnIP: returnIP,
		prefxLen: uint(pLen),
	}
}

func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// Download Speed

type DlSpeedCtl struct {
	UploaderIP   string   `json:"uploader-ip"`
	DownloaderIP string   `json:"downloader-ip"`
	MidstreamIPs []string `json:"midstream-ips"`
	Filesize     uint     `json:"filesize"` // Specified in bytes
}

func testDlSpeed(peers uint, filesize uint, netem NetemConfig) time.Duration {
	// Begin our allocation processes to get a
	// unique store path and IP for each host/daemon.
	//
	// Note: We don't care about returning IPs back to
	//       the allocator because each test gets a fresh
	//       allocation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dbAlloc := AllocateFile(ctx, "inu.*.db")
	ipAlloc := AllocateIPv4(ctx, "10.42.0.0/16")

	// We start the network, it is formed of:
	//   1. Bridge
	//   2. DHT pair
	//   3. Upload/download
	//   4. Midstream daemons
	//   5. Test controller
	n, err := g.NewNetwork(g.GenerateNetworkName())
	if err != nil {
		panic(err)
	}
	defer n.Close()

	// 1 - Provides switching capabilities
	br, err := n.AddSwitch("bridge")
	if err != nil {
		panic(err)
	}
	defer br.Close()

	// 2
	dhtAHost, dhtAIP := newHost("dht-a", ipAlloc, n, br)
	defer dhtAHost.Close()
	dhtBHost, dhtBIP := newHost("dht-b", ipAlloc, n, br)
	defer dhtBHost.Close()

	// 3 - Upload and download the test file from these respectively
	uploaderHost, uploaderIP := newHost("uploader", ipAlloc, n, br)
	defer uploaderHost.Close()
	downloaderHost, downloaderIP := newHost("downloader", ipAlloc, n, br)
	defer downloaderHost.Close()

	// 4 - Propagate the file before it is downloaded by the downloader
	midstreams := make([]*g.Host, peers)
	midstreamIPs := make([]string, peers)
	for i := range midstreams {
		h, ip := newHost(fmt.Sprintf("dmn-%d", i+1), ipAlloc, n, br)
		midstreams[i] = h
		midstreamIPs[i] = ip
		defer midstreams[i].Close()
	}

	// 5
	ctlHost, _ := newHost("controller", ipAlloc, n, br)
	defer ctlHost.Close()

	// Validate that the initial network components can connect to each other
	hosts := append(midstreams, dhtAHost, dhtBHost, uploaderHost, downloaderHost, ctlHost)
	retries := 0
	err = g.TestConnectivity(hosts...)
	for err != nil && !strings.Contains(err.Error(), "lost 1 packet") {
		if retries >= 30 {
			panic(fmt.Errorf("could not establish connectivity: %w", err))
		}
		time.Sleep(1 * time.Second)
		err = g.TestConnectivity(hosts...)
		retries += 1
	}

	// Start the network
	coptCtx := cmdopt.Context{Context: ctx}
	dhtNodesEnv := cmdopt.EnvVar("INU_DAEMON_DHT_NODES", fmt.Sprintf("[\"%s:3000\", \"%s:3000\"]", dhtAIP, dhtBIP))

	_, err = dhtAHost.StartGo("../inu", coptCtx, cmdopt.Args("dht", "start"),
		cmdopt.EnvVar("INU_NODE_ID", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"))
	if err != nil {
		panic(err)
	}
	time.Sleep(2 * time.Second) // Wait for DHT A to start before bootstrapping B
	_, err = dhtBHost.StartGo("../inu", coptCtx,
		cmdopt.EnvVar("INU_NODE_ID", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAQ"),
		cmdopt.Args("dht", "join", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", dhtAIP+":3000"))
	if err != nil {
		panic(err)
	}

	_, err = uploaderHost.StartGo("../inu", cmdopt.Args("peer", "daemon"), dhtNodesEnv, coptCtx,
		cmdopt.EnvVar("INU_DAEMON_STORE_PATH", <-dbAlloc.NewFile), cmdopt.EnvVar("INU_DAEMON_PUBLIC_IP", uploaderIP))
	if err != nil {
		panic(err)
	}
	_, err = downloaderHost.StartGo("../inu", cmdopt.Args("peer", "daemon"), dhtNodesEnv, coptCtx,
		cmdopt.EnvVar("INU_DAEMON_STORE_PATH", <-dbAlloc.NewFile), cmdopt.EnvVar("INU_DAEMON_PUBLIC_IP", downloaderIP))
	if err != nil {
		panic(err)
	}
	for i, m := range midstreams {
		_, err = m.StartGo("../inu", cmdopt.Args("peer", "daemon"), dhtNodesEnv, coptCtx,
			cmdopt.EnvVar("INU_DAEMON_STORE_PATH", <-dbAlloc.NewFile), cmdopt.EnvVar("INU_DAEMON_PUBLIC_IP", midstreamIPs[i]))
		if err != nil {
			panic(err)
		}
	}

	// Create the controller, start it, and begin communicating
	// with it via a unix domain socket
	sockAddr := fmt.Sprintf("/tmp/%s-sock", n.Name)
	if err := os.RemoveAll(sockAddr); err != nil {
		panic(err)
	}
	l, err := net.Listen("unix", sockAddr)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	dlCtl := DlSpeedCtl{
		UploaderIP:   uploaderIP,
		DownloaderIP: downloaderIP,
		MidstreamIPs: midstreamIPs,
		Filesize:     filesize,
	}
	dlCtlData, err := json.Marshal(dlCtl)
	if err != nil {
		panic(err)
	}

	outDur := new(bytes.Buffer)
	ctlCmd, err := ctlHost.StartGo("./inuctl", cmdopt.Args("dl-speed", sockAddr), coptCtx,
		cmdopt.Stdin(bytes.NewReader(dlCtlData)), cmdopt.Stdout(outDur), cmdopt.Stderr(os.Stderr))
	if err != nil {
		panic(err)
	}

	// Communicate with the controller
	conn, err := l.Accept()
	if err != nil {
		panic(err)
	}
	enc := gob.NewEncoder(conn)
	dec := gob.NewDecoder(conn)

	// Wait until it asks for netem to be applied,
	// then apply it and send an acknowledgement
	req := Request(0)
	if err := dec.Decode(&req); err != nil {
		panic(err)
	}
	if req != ReqNetem {
		panic("controller is not requesting netem")
	}
	for _, h := range append(midstreams, uploaderHost, downloaderHost) {
		applyNetem(h, netem)
	}
	if err := enc.Encode(AckNetem); err != nil {
		panic(err)
	}

	// Parse the duration output
	if err := ctlCmd.Wait(); err != nil {
		panic(err)
	}
	d, err := time.ParseDuration(outDur.String())
	if err != nil {
		panic(err)
	}

	return d
}

// Routing ability

type RoutingCtl struct {
	UploaderIP   string `json:"uploader-ip"`
	DownloaderIP string `json:"downloader-ip"`
	MidstreamIP  string `json:"midstream-ip"`
	Filesize     uint   `json:"filesize"` // Specified in bytes
}

type RoutingResult struct {
	MidstreamDl  int `json:"midstream-dl"`
	DownloaderDl int `json:"downloader-dl"`
}

func testRouting(filesize uint) RoutingResult {
	// This tests aims to see if the routing mechanisms
	// reduces load in intermediate paths if there is
	// already a peer on the local AS
	stopCapture := make(chan struct{})
	captured := make([]g.CapturePacket, 0)
	packets := make(chan g.CapturePacket)
	capture := g.NewCapture(
		cptopt.ToChannel(packets),
		cptopt.FilterExpression("tcp"),
		cptopt.FilterInterfaces(func(i *g.Interface) bool {
			return i.Name == "veth0" || i.Name == "veth1"
		}),
	)

	// Begin our allocation processes to get a
	// unique store path and IP for each host/daemon.
	//
	// Note: We don't care about returning IPs back to
	//       the allocator because each test gets a fresh
	//       allocation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dbAlloc := AllocateFile(ctx, "inu.*.db")
	ipAllocUpstream := AllocateIPv4(ctx, "10.42.0.0/24")
	ipAllocDownstream := AllocateIPv4(ctx, "10.42.1.0/24")
	asnTsv := "10.42.0.0/24\t1\n10.42.1.0/24\t2"

	// We start the network, it is formed of:
	//   - 3 Bridges
	//   - DHT pair
	//   - Upload/download
	//   - Midstream (which exist on the same network as the downloader)
	//   - Test controller
	n, err := g.NewNetwork(g.GenerateNetworkName())
	if err != nil {
		panic(err)
	}
	defer n.Close()

	// Upstream network
	brUpstream, err := n.AddSwitch("bridge-downstream")
	if err != nil {
		panic(err)
	}
	defer brUpstream.Close()
	dhtAHost, dhtAIP := newHost("dht-a", ipAllocUpstream, n, brUpstream)
	defer dhtAHost.Close()
	dhtBHost, dhtBIP := newHost("dht-b", ipAllocUpstream, n, brUpstream)
	defer dhtBHost.Close()
	uploaderHost, uploaderIP := newHost("uploader", ipAllocUpstream, n, brUpstream)
	defer uploaderHost.Close()
	upstreamHosts := []*g.Host{dhtAHost, dhtBHost, uploaderHost}

	// Downstream network
	brDownstream, err := n.AddSwitch("bridge-upstream")
	if err != nil {
		panic(err)
	}
	downloaderHost, downloaderIP := newHost("downloader", ipAllocDownstream, n, brDownstream)
	defer downloaderHost.Close()
	midstreamHost, midstreamIP := newHost("midstream", ipAllocDownstream, n, brDownstream)
	defer midstreamHost.Close()
	ctlHost, _ := newHost("controller", ipAllocDownstream, n, brDownstream)
	defer ctlHost.Close()
	downstreamHosts := []*g.Host{downloaderHost, midstreamHost, ctlHost}

	// Connect the two bridges network with an
	// intermediate router to simulate peering
	rtrUpstreamIP := <-ipAllocUpstream.NewIP
	rtrDownstreamIP := <-ipAllocDownstream.NewIP
	rtrIXP, err := n.AddRouter("router-ixp",
		g.NewInterface("veth0", brUpstream,
			opt.AddressIP(fmt.Sprintf("%s/%d", rtrUpstreamIP, ipAllocUpstream.PrefixLen()))),
		g.NewInterface("veth1", brDownstream,
			opt.AddressIP(fmt.Sprintf("%s/%d", rtrDownstreamIP, ipAllocDownstream.PrefixLen()))),
		capture,
	)
	if err != nil {
		panic(err)
	}

	// Add the default routes for each host so
	// that they know to query the router as
	// the gateway
	for _, h := range upstreamHosts {
		if err := h.AddDefaultRoute(net.ParseIP(rtrUpstreamIP)); err != nil {
			panic(err)
		}
	}
	for _, h := range downstreamHosts {
		if err := h.AddDefaultRoute(net.ParseIP(rtrDownstreamIP)); err != nil {
			panic(err)
		}
	}

	// Start the capture
	go func() {
		for p := range packets {
			select {
			case <-stopCapture:
				return
			default:
			}

			captured = append(captured, p)
		}
	}()

	// Validate that the initial network components can connect to each other
	hosts := []*g.Host{dhtAHost, dhtBHost, uploaderHost, rtrIXP.Host, downloaderHost, midstreamHost, ctlHost}
	if err := g.TestConnectivity(hosts...); err != nil {
		panic(err)
	}

	// Start the network
	coptCtx := cmdopt.Context{Context: ctx}

	dhtNodesEnv := cmdopt.EnvVar("INU_DAEMON_DHT_NODES", fmt.Sprintf("[\"%s:3000\", \"%s:3000\"]", dhtAIP, dhtBIP))

	_, err = dhtAHost.StartGo("../inu", coptCtx,
		cmdopt.Args("dht", "start", "--asn", "-"), cmdopt.Stdin(strings.NewReader(asnTsv)),
		cmdopt.EnvVar("INU_NODE_ID", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"))
	if err != nil {
		panic(err)
	}
	time.Sleep(2 * time.Second) // Wait for DHT A to start before bootstrapping B
	_, err = dhtBHost.StartGo("../inu", coptCtx, cmdopt.Stdin(strings.NewReader(asnTsv)),
		cmdopt.EnvVar("INU_NODE_ID", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAQ"),
		cmdopt.Args("dht", "join", "--asn", "-", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", dhtAIP+":3000"))
	if err != nil {
		panic(err)
	}

	_, err = uploaderHost.StartGo("../inu", cmdopt.Args("peer", "daemon"), dhtNodesEnv, coptCtx,
		cmdopt.EnvVar("INU_DAEMON_STORE_PATH", <-dbAlloc.NewFile), cmdopt.EnvVar("INU_DAEMON_PUBLIC_IP", uploaderIP))
	if err != nil {
		panic(err)
	}
	_, err = downloaderHost.StartGo("../inu", cmdopt.Args("peer", "daemon"), dhtNodesEnv, coptCtx,
		cmdopt.EnvVar("INU_DAEMON_STORE_PATH", <-dbAlloc.NewFile), cmdopt.EnvVar("INU_DAEMON_PUBLIC_IP", downloaderIP))
	if err != nil {
		panic(err)
	}
	_, err = midstreamHost.StartGo("../inu", cmdopt.Args("peer", "daemon"), dhtNodesEnv, coptCtx,
		cmdopt.EnvVar("INU_DAEMON_STORE_PATH", <-dbAlloc.NewFile), cmdopt.EnvVar("INU_DAEMON_PUBLIC_IP", midstreamIP))
	if err != nil {
		panic(err)
	}

	rtCtl := RoutingCtl{
		UploaderIP:   uploaderIP,
		DownloaderIP: downloaderIP,
		MidstreamIP:  midstreamIP,
		Filesize:     filesize * 1024 * 1024,
	}
	rtCtlData, err := json.Marshal(rtCtl)
	if err != nil {
		panic(err)
	}

	_, err = ctlHost.RunGo("./inuctl", cmdopt.Args("routing"), coptCtx,
		cmdopt.Stdin(bytes.NewReader(rtCtlData)), cmdopt.Stderr(os.Stderr))
	if err != nil {
		panic(err)
	}

	// Flush remaining packets in the capture
	time.Sleep(2 * time.Second)
	if err := capture.Flush(); err != nil {
		panic(err)
	}
	stopCapture <- struct{}{}

	// Perform packet analysis
	return RoutingResult{
		MidstreamDl:  measureLoad(captured, midstreamIP, uploaderIP),
		DownloaderDl: measureLoad(captured, downloaderIP, uploaderIP),
	}
}

func measureLoad(ps []g.CapturePacket, ipA, ipB string) int {
	size := 0

	for _, p := range ps {
		pp := p.Decode(gopacket.Default)

		ipLayer := pp.Layer(layers.LayerTypeIPv4)
		if ipLayer == nil {
			continue
		}
		ip, _ := ipLayer.(*layers.IPv4)

		a := ip.SrcIP.String() == ipA && ip.DstIP.String() == ipB
		b := ip.SrcIP.String() == ipB && ip.DstIP.String() == ipA
		if a || b {
			size += p.Length
		}
	}

	return size
}

// Churn resistance

type ChurnCtl struct {
	CIDR           string
	K, Alpha       int
	AvgNodesOnline int
	MeanUptime     time.Duration
	SimLength      time.Duration
}

type ChurnResult struct {
	SuccessRate     float64 `json:"success-rate"`
	MeanTrafficLoad float64 `json:"mean-traffic-load"`
}

func testChurn(ctl ChurnCtl) ChurnResult {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up the packet capture

	packets := make(chan g.CapturePacket)
	capture := g.NewCapture(
		cptopt.ToChannel(packets),
		cptopt.FilterExpression("tcp"),
	)

	// Set up the network

	n, err := g.NewNetwork(g.GenerateNetworkName(), capture)
	if err != nil {
		panic(err)
	}
	defer n.Close()
	churner, err := n.AddHost("churner")
	if err != nil {
		panic(err)
	}

	idx := churner.Interfaces[0].Link.Attrs().Index
	_, prefix, err := net.ParseCIDR(ctl.CIDR)
	if err != nil {
		panic(err)
	}
	err = churner.AddRoute(&netlink.Route{
		LinkIndex: idx,
		Scope:     unix.RT_SCOPE_HOST,
		Dst:       prefix,
		Protocol:  unix.RTPROT_BOOT,
		Family:    unix.AF_INET,
		Table:     unix.RT_TABLE_LOCAL,
		Type:      unix.RTN_LOCAL,
	})
	if err != nil {
		panic(err)
	}

	// Run packet capture and churning

	seen := uint64(0)
	nodes := make(map[string]struct{})
	go func() {
		// In the churn tests, the bootstrap IP is the
		// first IP the allocator returns, we ignore
		// if this is the destination IP since that means
		//
		bootstrap := <-AllocateIPv4(ctx, ctl.CIDR).NewIP

		for p := range packets {
			// Parse the packet
			pp := p.Decode(gopacket.Default)

			// Get IP information from the packet
			l := pp.Layer(layers.LayerTypeIPv4)
			if l == nil {
				continue
			}
			ipL := l.(*layers.IPv4)

			// Ignore requests to the bootstrap IP
			// which are either:
			//   - Bootstrap requests
			//   - Client query requests
			if ipL.DstIP.String() == bootstrap {
				continue
			}

			// Keep track of how many bytes we've
			// and how many unique nodes we've
			// seen
			seen += uint64(p.Length)
			nodes[ipL.SrcIP.String()] = struct{}{}
			nodes[ipL.DstIP.String()] = struct{}{}
		}
	}()

	churnCtlData, err := json.Marshal(ctl)
	if err != nil {
		panic(err)
	}
	coptCtx := cmdopt.Context{Context: ctx}
	out := new(bytes.Buffer)
	_, err = churner.RunGo("./inuctl", cmdopt.Args("churn"), coptCtx,
		cmdopt.Stdin(bytes.NewReader(churnCtlData)), cmdopt.Stderr(os.Stderr), cmdopt.Stdout(out))
	if err != nil {
		panic(err)
	}

	// Parse the success rate and the mean traffic load

	time.Sleep(2 * time.Second) // Wait for capture to finish
	if err := capture.Flush(); err != nil {
		panic(err)
	}
	close(packets)

	r, err := strconv.ParseFloat(out.String(), 64)
	if err != nil {
		panic(err)
	}
	return ChurnResult{
		SuccessRate:     r,
		MeanTrafficLoad: (float64(seen) / float64(len(nodes))) / ctl.SimLength.Seconds(),
	}
}

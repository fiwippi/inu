package main

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/exp/rand"
	"gonum.org/v1/gonum/stat/distuv"

	"inu"
	"inu/cmd/evaluate"
	"inu/dht"
)

const clearLine = "\033[2K\r"

func init() {
	// Turn off the default slog logger
	nilLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	slog.SetDefault(nilLogger)

	// Turn off chi logging
	middleware.DefaultLogger = func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}

func main() {
	flag.Parse()

	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}

	switch flag.Arg(0) {
	case "dl-speed":
		ensureNArgs(2)
		ctlDlSpeed(stdin, flag.Arg(1))
	case "routing":
		ensureNArgs(1)
		ctlRouting(stdin)
	case "churn":
		ensureNArgs(1)
		ctlChurn(stdin)
	default:
		panic("invalid control specifier")
	}
}

func ensureNArgs(n int) {
	if flag.NArg() != n {
		panic("incorrect number of args")
	}
}

// Churn

func ctlChurn(ctlData []byte) {
	ctl := evaluate.ChurnCtl{}
	if err := json.Unmarshal(ctlData, &ctl); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// -- Setup

	ip := evaluate.AllocateIPv4(ctx, ctl.CIDR)

	// All nodes have the same K and Alpha
	testConfig := dht.DefaultNodeConfig()
	testConfig.K = ctl.K
	testConfig.Alpha = ctl.Alpha

	// Create initial bootstrap node (always online)
	// and start it, so it initialises its own contact
	// details (we need to know the port to create
	// the rest of the nodes)
	bootstrapConfig := testConfig
	bootstrapConfig.ID = dht.ParseUint64(0)
	bootstrapConfig.Host = <-ip.NewIP
	bootstrap, err := dht.NewNode(bootstrapConfig)
	if err != nil {
		panic(err)
	}

	// Create DHT nodes, in total there are avgNodesOnline
	// nodes currently online such that:
	//   - A, these initially start online
	//   - B, these initially start offline
	keyRand := rand.New(rand.NewSource(0))
	nodeKeys := make([]dht.Key, ctl.AvgNodesOnline*2)
	for i := range nodeKeys {
		nodeKeys[i] = randKey(keyRand)
	}

	durChan := sessionTimes(ctx, ctl.MeanUptime) // Node uptime/downtime specified by exponential distribution
	A := make([]*churnNode, ctl.AvgNodesOnline)
	B := make([]*churnNode, ctl.AvgNodesOnline)
	for i := range ctl.AvgNodesOnline {
		A[i] = newChurnNode(testConfig.WithID(nodeKeys[i]).WithHost(<-ip.NewIP),
			bootstrap.Contact(), durChan)
		B[i] = newChurnNode(testConfig.WithID(nodeKeys[i+ctl.AvgNodesOnline]).WithHost(<-ip.NewIP),
			bootstrap.Contact(), durChan)
	}

	// -- Simulation

	// Start the boostrap node
	fmt.Fprintf(os.Stderr, "%sbootstrapping", clearLine)
	if err := bootstrap.Start(); err != nil {
		panic(err)
	}

	// Boostrap all the initial online nodes to the network
	for i, cn := range A {
		cn.mustStart(ctx)
		fmt.Fprintf(os.Stderr, "%sbootstrapping %d/%d", clearLine, i+1, len(A))
		time.Sleep(50 * time.Millisecond)
	}

	// Store keys on the DHT, keys are stored
	// on the online nodes which are selected
	// randomly
	peers := []dht.Peer{{
		Port:      60,
		ASN:       60,
		Published: time.Now().UTC(),
	}}
	keys := make([]dht.Key, ctl.AvgNodesOnline)
	for i := range keys {
		keys[i] = randKey(keyRand)
		cn := A[keyRand.Intn(len(A))]
		if err := cn.node.Store(keys[i], peers, false); err != nil {
			panic(err)
		}
		fmt.Fprintf(os.Stderr, "%sstoring keys %d/%d", clearLine, i+1, len(keys))
		time.Sleep(25 * time.Millisecond)
	}

	// Wait for the DHT to stabilise, i.e.:
	//   - Routing tables propagate
	//   - Stored keys propagate
	fmt.Fprintf(os.Stderr, "%sstabilising", clearLine)
	time.Sleep(3 * time.Second)

	// Begin churn for all nodes
	for _, cn := range A {
		go cn.churnA(ctx)
	}
	for _, cn := range B {
		go cn.churnB(ctx)
	}

	// Track the rate of successful requests
	successes := atomic.Uint64{}
	total := atomic.Uint64{}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			randomKey := keys[keyRand.Intn(len(keys))]
			ps, err := bootstrap.FindPeers(randomKey)
			if err == nil && ps[0] == peers[0] {
				successes.Add(1)
			}
			total.Add(1)

			time.Sleep(50 * time.Millisecond)
		}
	}()

	go func() {
		start := time.Now()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			fmt.Fprintf(os.Stderr, "%srunning - approx %s/%s",
				clearLine, time.Since(start).Round(time.Second), ctl.SimLength)
			time.Sleep(1 * time.Second)
		}
	}()

	<-time.After(ctl.SimLength)
	cancel()

	// Stop all DHT nodes
	if err := bootstrap.Stop(); err != nil {
		panic(err)
	}
	for _, cn := range A {
		cn.mustStop()
	}
	for _, cn := range B {
		cn.mustStop()
	}
	fmt.Fprint(os.Stderr, clearLine, "done")

	fmt.Printf("%.2f", float64(successes.Load())/float64(total.Load())*100)
}

type churnNode struct {
	node         *dht.Node
	config       dht.NodeConfig
	bootstrap    dht.Contact
	nextDuration <-chan time.Duration
}

func newChurnNode(config dht.NodeConfig, bootstrap dht.Contact, durChan <-chan time.Duration) *churnNode {
	n, err := dht.NewNode(config)
	if err != nil {
		panic(err)
	}

	return &churnNode{
		node:         n,
		config:       config,
		bootstrap:    bootstrap,
		nextDuration: durChan,
	}
}

func (n *churnNode) mustStart(ctx context.Context) {
	var err error
	n.node, err = dht.NewNode(n.config) // Node fully recreated to simulate loss of data
	if err != nil {
		panic(err)
	}

	err = n.node.Start()
	for err != nil {
		select {
		case <-ctx.Done():
			return
		default:
		}

		panic(err)
	}

	err = n.node.Bootstrap(n.bootstrap)
	for err != nil {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err = n.node.Bootstrap(n.bootstrap)
		time.Sleep(1 * time.Second)
	}
}

func (n *churnNode) mustStop() {
	if err := n.node.Stop(); err != nil {
		panic(err)
	}
}

func (n *churnNode) churnA(ctx context.Context) {
	for {
		// Online --> Offline
		select {
		case <-ctx.Done():
			return
		case <-time.After(<-n.nextDuration):
		}
		n.mustStop()

		// Offline --> Online
		select {
		case <-ctx.Done():
			return
		case <-time.After(<-n.nextDuration):
		}
		n.mustStart(ctx)
	}
}

func (n *churnNode) churnB(ctx context.Context) {
	for {
		// Offline --> Online
		select {
		case <-ctx.Done():
			return
		case <-time.After(<-n.nextDuration):
		}
		n.mustStart(ctx)

		// Online --> Offline
		select {
		case <-ctx.Done():
			return
		case <-time.After(<-n.nextDuration):
		}
		n.mustStop()
	}
}

func sessionTimes(ctx context.Context, meanDuration time.Duration) <-chan time.Duration {
	durChan := make(chan time.Duration)

	go func() {
		src := rand.NewSource(0)
		ed := distuv.Exponential{
			Rate: 1 / meanDuration.Seconds(),
			Src:  src,
		}

		for {
			s := ed.Rand()
			for s < 1 {
				s = ed.Rand()
			}

			select {
			case <-ctx.Done():
				return
			case durChan <- time.Duration(s) * time.Second:
			}
		}
	}()

	return durChan
}

// Download speed

func ctlDlSpeed(ctlData []byte, sockAddr string) {
	// Parse the control data
	ctl := evaluate.DlSpeedCtl{}
	if err := json.Unmarshal(ctlData, &ctl); err != nil {
		panic(err)
	}

	// Create the APIs to control the daemons
	upstreamAPI := newAPI(ctl.UploaderIP)
	downstreamAPI := newAPI(ctl.DownloaderIP)
	midstreamAPIs := make([]*inu.API, 0)
	for _, ip := range ctl.MidstreamIPs {
		midstreamAPIs = append(midstreamAPIs, newAPI(ip))
	}

	// Bind to the unix domain socket for IPC
	conn, err := net.Dial("unix", sockAddr)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	enc := gob.NewEncoder(conn)
	dec := gob.NewDecoder(conn)

	// Upload the file to the DHT from the uploader
	c, err := upstreamAPI.AddBytes(newFile(ctl.Filesize))
	if err != nil {
		panic(err)
	}
	if _, err := upstreamAPI.Upload(string(c)); err != nil {
		panic(err)
	}

	// Now propagate the file to every midstream daemon
	wg := sync.WaitGroup{}
	sem := make(chan struct{}, 10)
	for _, api := range midstreamAPIs {
		api := api

		wg.Add(1)
		go func() {
			defer wg.Done()

			sem <- struct{}{}
			if err := api.Download(c); err != nil {
				panic(err)
			}
			<-sem
		}()
	}
	wg.Wait()

	// Tell the network manager to apply the netem
	// configuration to all interfaces
	if err := enc.Encode(evaluate.ReqNetem); err != nil {
		panic(err)
	}
	var a evaluate.Ack
	if err := dec.Decode(&a); err != nil {
		panic(err)
	}
	if a != evaluate.AckNetem {
		panic("network manager did not apply netem")
	}

	// Finally we measure how long it takes for the final
	// peer to download the file
	start := time.Now()
	if err := downstreamAPI.Download(c); err != nil {
		panic(err)
	}
	fmt.Print(time.Since(start))
}

// Routing

func ctlRouting(ctlData []byte) {
	// Parse the control data
	ctl := evaluate.RoutingCtl{}
	if err := json.Unmarshal(ctlData, &ctl); err != nil {
		panic(err)
	}

	// Create the APIs to control the daemons
	upstreamAPI := newAPI(ctl.UploaderIP)
	downstreamAPI := newAPI(ctl.DownloaderIP)
	midstreamAPI := newAPI(ctl.MidstreamIP)

	// Upload the file to the DHT from the uploader
	c, err := upstreamAPI.AddBytes(newFile(ctl.Filesize))
	if err != nil {
		panic(err)
	}
	if _, err := upstreamAPI.Upload(string(c)); err != nil {
		panic(err)
	}

	// Midstream download
	if err := midstreamAPI.Download(c); err != nil {
		panic(err)
	}

	// Downloader download
	if err := downstreamAPI.Download(c); err != nil {
		panic(err)
	}
}

// Helpers

func newFile(size uint) []byte {
	if size == 0 {
		panic("must specify non-zero size")
	}

	// Create a random file
	r := rand.New(rand.NewSource(0))
	f := make([]byte, size)
	n, err := r.Read(f)
	if err != nil {
		panic(err)
	}
	if n != len(f) {
		panic("did not read into all of data")
	}
	return f
}

func newAPI(ip string) *inu.API {
	retries := 0
	p := inu.DefaultDaemonConfig().RpcPort
	a, err := inu.NewAPI(ip, p)
	for err != nil {
		if retries > 30 {
			panic(err)
		}
		time.Sleep(1 * time.Second)
		a, err = inu.NewAPI(ip, p)
		retries += 1
	}

	if err != nil {
		panic(err)
	}
	return a
}

func randKey(r *rand.Rand) dht.Key {
	k := dht.Key{}
	for i := range k {
		if r.Int31()%2 == 0 {
			k[i] = 1
		}
	}
	return k
}

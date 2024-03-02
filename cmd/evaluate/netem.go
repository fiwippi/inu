package main

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/florianl/go-tc"
	"github.com/florianl/go-tc/core"
	"github.com/jsimonetti/rtnetlink"
	"golang.org/x/sys/unix"
)

// tc

type tcConfig struct {
	latency time.Duration
	jitter  time.Duration
	rate    uint64
	loss    uint32
}

func (c tcConfig) Attribute() tc.Attribute {
	// go-tc multiplies the rate by 8, so we divide it beforehand,
	// e.g. if we specify it as 8, go-tc inputs it as 64
	r := c.rate / 8

	return tc.Attribute{
		Kind: "netem",
		Netem: &tc.Netem{
			Qopt: tc.NetemQopt{
				Latency: durToTick(c.latency),
				Loss:    c.loss * 42949673, // 42949673 = 1%
				Jitter:  durToTick(c.jitter),
			},
			Rate64: &r,
		},
	}
}

// Netem

type netem struct {
	// Configuration
	ifaceName string
	cidr      string
	tc        tcConfig

	// Kernel APIs
	rtnl  *rtnetlink.Conn
	tcnl  *tc.Tc
	qdisc tc.Object

	// IP Allocation
	newIP    chan string
	returnIP chan string
}

func newNetem(ifaceName, cidr string, config tcConfig) *netem {
	return &netem{
		ifaceName: ifaceName,
		cidr:      cidr,
		tc:        config,
		newIP:     make(chan string),
		returnIP:  make(chan string),
	}
}

func (n *netem) start() error {
	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return err
	}
	n.rtnl = conn

	// Create the test interface
	err = n.rtnl.Link.New(&rtnetlink.LinkMessage{
		Family: unix.AF_INET,
		Type:   unix.ARPHRD_NETROM,
		Index:  0,
		Flags:  unix.IFF_UP,
		Change: unix.IFF_UP,
		Attributes: &rtnetlink.LinkAttributes{
			Name: n.ifaceName,
			Info: &rtnetlink.LinkInfo{Kind: "dummy"},
		},
	})
	if err != nil && !strings.Contains(err.Error(), "netlink receive: file exists") {
		return err
	}

	// Route addresses to it using AnyIP
	_, prefix, err := net.ParseCIDR(n.cidr)
	if err != nil {
		return err
	}
	pSize, _ := prefix.Mask.Size()
	iface, err := net.InterfaceByName(n.ifaceName)
	if err != nil {
		return err
	}
	err = conn.Route.Replace(&rtnetlink.RouteMessage{
		Family:    unix.AF_INET,
		Table:     unix.RT_TABLE_LOCAL,
		Protocol:  unix.RTPROT_BOOT,
		Scope:     unix.RT_SCOPE_HOST,
		Type:      unix.RTN_LOCAL,
		DstLength: uint8(pSize),
		Attributes: rtnetlink.RouteAttributes{
			Dst:      prefix.IP,
			OutIface: uint32(iface.Index),
		},
	})

	// Emulate network conditions on the interface
	tcnl, err := tc.Open(&tc.Config{})
	if err != nil {
		return err
	}
	n.tcnl = tcnl

	n.qdisc = tc.Object{
		Msg: tc.Msg{
			Family:  unix.AF_INET,
			Ifindex: uint32(iface.Index),
			Handle:  core.BuildHandle(0x1, 0x0), // Name used to refer to the qdisc
			Parent:  tc.HandleRoot,
			Info:    0,
		},
		Attribute: n.tc.Attribute(),
	}

	// qdisc replace doesn't overwrite files correctly so
	// this haphazard method is implemented
	if err := n.tcnl.Qdisc().Add(&n.qdisc); err != nil {
		if !strings.Contains(err.Error(), "file exists") {
			return err
		}

		// Delete the existing qdisc and add the new one
		if err := n.tcnl.Qdisc().Delete(&n.qdisc); err != nil {
			return err
		}
		if err := n.tcnl.Qdisc().Add(&n.qdisc); err != nil {
			return err
		}
	}

	// Begin IP allocation
	r := cidrRange(n.cidr)
	ipLock := sync.Mutex{}

	go func() {
		for {
			// This does not protect against cases where
			// we have no more IPs to allocate!
			ipLock.Lock()
			ip := r[0]
			r = r[1:]
			ipLock.Unlock()

			select {
			case n.newIP <- ip:
			}
		}
	}()

	go func() {
		for {
			select {
			case ip := <-n.returnIP:
				ipLock.Lock()
				r = append(r, ip)
				ipLock.Unlock()
			}
		}
	}()

	return nil
}

func (n *netem) stop() error {
	// Delete our interface
	iface, err := net.InterfaceByName(n.ifaceName)
	if err != nil {
		return err
	}
	if err := n.rtnl.Link.Delete(uint32(iface.Index)); err != nil {
		return err
	}

	// Delete our netem qdisc
	if err := n.tcnl.Qdisc().Delete(&n.qdisc); err != nil {
		return err
	}

	// Close tc and RTNETLINK
	if err := n.tcnl.Close(); err != nil {
		return err
	}
	return n.rtnl.Close()
}

// Config helpers

func cidrRange(cidr string) []string {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}

	ips := make([]string, 0)
	for ip := ip.Mask(ipNet.Mask); ipNet.Contains(ip); incIP(ip) {
		ips = append(ips, ip.String())
	}

	// Remove network address (the first one)
	// and broadcast address (the last one)
	return ips[1 : len(ips)-1]
}

func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func durToTick(d time.Duration) uint32 {
	t, err := core.Duration2TcTime(d)
	if err != nil {
		panic(err)
	}
	return core.Time2Tick(t)
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/exp/rand"
	"gonum.org/v1/gonum/stat/distuv"

	"inu/dht"
)

func simulateChurn(k, alpha, avgNodesOnline int, meanUptime, simLength time.Duration) (float64, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// -- Setup

	// All nodes have the same K and Alpha
	testConfig := dht.DefaultNodeConfig()
	testConfig.K = k
	testConfig.Alpha = alpha

	// Generate enough free ports for all nodes
	ports := freePorts(2*avgNodesOnline + 1)

	// Create initial bootstrap node (always online)
	bootstrapConfig := testConfig
	bootstrapConfig.ID = dht.ParseUint64(0)
	bootstrapConfig.Port = ports[0]
	bootstrap, err := dht.NewNode(bootstrapConfig)
	if err != nil {
		return 0, err
	}

	// Create DHT nodes, in total there are avgNodesOnline
	// nodes currently online such that:
	//   - A, these initially start online
	//   - B, these initially start offline
	keyRand := rand.New(rand.NewSource(0))
	nodeKeys := make([]dht.Key, avgNodesOnline*2)
	for i := range nodeKeys {
		nodeKeys[i] = randKey(keyRand)
	}

	durChan := exponentialDuration(ctx, meanUptime) // Node uptime/downtime specified by exponential distribution
	A := make([]*churnNode, avgNodesOnline)
	B := make([]*churnNode, avgNodesOnline)

	for i := range avgNodesOnline {
		aConf := testConfig.WithID(nodeKeys[i]).WithPort(ports[i+1])
		A[i] = newChurnNode(aConf, bootstrap.Contact(), durChan)
		bConf := testConfig.WithID(nodeKeys[i+avgNodesOnline]).WithPort(ports[i+1+avgNodesOnline])
		B[i] = newChurnNode(bConf, bootstrap.Contact(), durChan)
	}

	// -- Simulation

	// Run the bootstrap node
	fmt.Printf("\rbootstrapping")
	if err := bootstrap.Start(); err != nil {
		return 0, err
	}
	defer bootstrap.Stop()

	// Boostrap all the initial online nodes to the network
	for i, cn := range A {
		cn.start()
		fmt.Printf("\rbootstrapping %d/%d", i+1, len(A))
		time.Sleep(150 * time.Millisecond)
	}

	// Store 1000 keys on the DHT,
	// keys are stored on the online nodes
	// which are selected randomly
	peers := []dht.Peer{{
		Port:      60,
		ASN:       60,
		Published: time.Now().UTC(),
	}}
	keys := make([]dht.Key, 1000)
	for i := range keys {
		keys[i] = randKey(keyRand)
		cn := A[keyRand.Intn(len(A))]
		if err := cn.node.Store(keys[i], peers); err != nil {
			return 0, err
		}
		fmt.Printf("\rstoring keys %d/%d", i+1, len(keys))
		time.Sleep(25 * time.Millisecond)
	}

	// Wait for the DHT to stabilise, i.e.:
	//   - Routing tables propagate
	//   - Stored keys propagate
	fmt.Printf("\rstabilising")
	time.Sleep(5 * time.Second)

	// Begin churn for all nodes
	for _, cn := range A {
		go cn.churnOffline(ctx)
	}
	for _, cn := range B {
		go cn.churnOnline(ctx)
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

			fmt.Printf("\rrunning - approx %s/%s", time.Since(start).Round(time.Second), simLength)
			time.Sleep(1 * time.Second)
		}
	}()

	<-time.After(simLength)

	fmt.Printf("\r")

	return float64(successes.Load()) / float64(total.Load()) * 100, nil
}

func testKVariance(ks []int, alpha int, meanSessionTimes []int, avgNodesOnline int, simLength time.Duration) error {
	// k --> { mean uptime --> success rate }
	result := make(map[int]map[int]float64)
	for _, k := range ks {
		result[k] = make(map[int]float64)

		for _, mst := range meanSessionTimes {
			uptime := time.Duration(mst) * time.Second

			rate, err := simulateChurn(k, alpha, avgNodesOnline, uptime, simLength)
			if err != nil {
				fmt.Printf("k = %d, mst = %d, failed: %s\n ", k, mst, err)
			} else {
				result[k][mst] = rate
				fmt.Printf("k = %d, mst = %d, rate = %.2f\n ", k, mst, rate)
			}
		}
	}

	f, err := os.Create("k-variance.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func testAlphaVariance(alphas []int, k int, meanSessionTimes []int, avgNodesOnline int, simLength time.Duration) error {
	// alpha --> { mean uptime --> success rate }
	result := make(map[int]map[int]float64)
	for _, alpha := range alphas {
		result[k] = make(map[int]float64)

		for _, mst := range meanSessionTimes {
			uptime := time.Duration(mst) * time.Second

			rate, err := simulateChurn(k, alpha, avgNodesOnline, uptime, simLength)
			if err != nil {
				fmt.Printf("alpha = %d, mst = %d, failed: %s\n ", alpha, mst, err)
			} else {
				result[k][mst] = rate
				fmt.Printf("alpha = %d, mst = %d, rate = %.2f\n ", alpha, mst, rate)
			}
		}
	}

	f, err := os.Create("alpha-variance.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// Nodes which are capable of churning

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

func (n *churnNode) start() {
	var err error
	n.node, err = dht.NewNode(n.config) // Node fully recreated to simulate loss of data
	if err != nil {
		panic(err)
	}

	if err := n.node.Start(); err != nil {
		for err != nil {
			if !strings.Contains(err.Error(), "bind: address already in use") {
				fmt.Println("ERR start:", n.node.Contact(), err)
				panic(err)
			}
			err = n.node.Start()
			time.Sleep(1 * time.Second)
		}
	}

	err = n.node.Bootstrap(n.bootstrap)
	for err != nil {
		err = n.node.Bootstrap(n.bootstrap)
	}
}

func (n *churnNode) churnOnline(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(<-n.nextDuration):
	}

	n.start()

	n.churnOffline(ctx)
}

func (n *churnNode) churnOffline(ctx context.Context) {
	// Online --> Offline
	select {
	case <-ctx.Done():
		n.node.Stop()
		return
	case <-time.After(<-n.nextDuration):
	}

	if err := n.node.Stop(); err != nil {
		panic(err)
	}

	n.churnOnline(ctx)
}

// Key generation

func randKey(r *rand.Rand) dht.Key {
	k := dht.Key{}
	for i := range k {
		if r.Int31()%2 == 0 {
			k[i] = 1
		}
	}
	return k
}

// Generation of values with a specific distribution

func exponentialDuration(ctx context.Context, meanDuration time.Duration) <-chan time.Duration {
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

// Port detection

func freePorts(count int) []uint16 {
	ps := make([]uint16, count)
	for i := range count {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			panic(err)
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			panic(err)
		}
		defer l.Close()

		ps[i] = uint16(l.Addr().(*net.TCPAddr).Port)
	}

	return ps
}

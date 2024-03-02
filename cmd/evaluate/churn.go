package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/exp/rand"
	"gonum.org/v1/gonum/stat/distuv"

	"inu/dht"
)

const clearLine = "\033[2K\r"

// Churn simulation

func simulateChurn(k, alpha, avgNodesOnline int, meanUptime, simLength time.Duration, nm *netem) (float64, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// -- Setup

	// All nodes have the same K and Alpha
	testConfig := dht.DefaultNodeConfig()
	testConfig.K = k
	testConfig.Alpha = alpha
	testConfig.Host = "10.41.0.0"

	// Create initial bootstrap node (always online)
	// and start it, so it initialises its own contact
	// details (we need to know the port to create
	// the rest of the nodes)
	bootstrapConfig := testConfig
	bootstrapConfig.ID = dht.ParseUint64(0)
	bHost := <-nm.newIP
	bootstrapConfig.Host = bHost
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
		A[i] = newChurnNode(testConfig.WithID(nodeKeys[i]).WithHost(<-nm.newIP),
			bootstrap.Contact(), durChan)
		B[i] = newChurnNode(testConfig.WithID(nodeKeys[i+avgNodesOnline]).WithHost(<-nm.newIP),
			bootstrap.Contact(), durChan)
	}

	// -- Simulation

	// Start the boostrap node
	fmt.Printf("%sbootstrapping", clearLine)
	if err := bootstrap.Start(); err != nil {
		return 0, err
	}

	// Boostrap all the initial online nodes to the network
	for i, cn := range A {
		cn.mustStart(ctx)
		fmt.Printf("%sbootstrapping %d/%d", clearLine, i+1, len(A))
		time.Sleep(150 * time.Millisecond)
	}

	// Store keys on the DHT, keys are stored
	// on the online nodes which are selected
	// randomly
	peers := []dht.Peer{{
		Port:      60,
		ASN:       60,
		Published: time.Now().UTC(),
	}}
	keys := make([]dht.Key, avgNodesOnline)
	for i := range keys {
		keys[i] = randKey(keyRand)
		cn := A[keyRand.Intn(len(A))]
		if err := cn.node.Store(keys[i], peers); err != nil {
			return 0, err
		}
		fmt.Printf("%sstoring keys %d/%d", clearLine, i+1, len(keys))
		time.Sleep(25 * time.Millisecond)
	}

	// Wait for the DHT to stabilise, i.e.:
	//   - Routing tables propagate
	//   - Stored keys propagate
	fmt.Printf("%sstabilising", clearLine)
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

			fmt.Printf("%srunning - approx %s/%s", clearLine, time.Since(start).Round(time.Second), simLength)
			time.Sleep(1 * time.Second)
		}
	}()

	<-time.After(simLength)
	cancel()

	// Stop all DHT nodes and return their ports
	if err := bootstrap.Stop(); err != nil {
		return 0, err
	}
	nm.returnIP <- bHost

	for _, cn := range A {
		cn.mustStop()
		nm.returnIP <- cn.config.Host
	}
	for _, cn := range B {
		cn.mustStop()
		nm.returnIP <- cn.config.Host
	}

	fmt.Print(clearLine, "done")

	return float64(successes.Load()) / float64(total.Load()) * 100, nil
}

func testKVariance(ks []int, alpha int, meanSessionTimes []int, avgNodesOnline int, simLength time.Duration, nm *netem) error {
	// k --> { mean uptime --> success rate }
	result := make(map[int]map[int]float64)
	for _, k := range ks {
		result[k] = make(map[int]float64)

		for _, mst := range meanSessionTimes {
			uptime := time.Duration(mst) * time.Second

			rate, err := simulateChurn(k, alpha, avgNodesOnline, uptime, simLength, nm)
			if err != nil {
				fmt.Printf("%sk = %d, mst = %d, failed: %s\n", clearLine, k, mst, err)
			} else {
				result[k][mst] = rate
				fmt.Printf("%sk = %d, mst = %d, rate = %.2f\n", clearLine, k, mst, rate)
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

func testAlphaVariance(alphas []int, k int, meanSessionTimes []int, avgNodesOnline int, simLength time.Duration, nm *netem) error {
	// alpha --> { mean uptime --> success rate }
	result := make(map[int]map[int]float64)
	for _, alpha := range alphas {
		result[k] = make(map[int]float64)

		for _, mst := range meanSessionTimes {
			uptime := time.Duration(mst) * time.Second

			rate, err := simulateChurn(k, alpha, avgNodesOnline, uptime, simLength, nm)
			if err != nil {
				fmt.Printf("%salpha = %d, mst = %d, failed: %s\n", clearLine, alpha, mst, err)
			} else {
				result[k][mst] = rate
				fmt.Printf("%salpha = %d, mst = %d, rate = %.2f\n", clearLine, alpha, mst, rate)
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

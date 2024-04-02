package evaluate

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	g "cunicu.li/gont/v2/pkg"
)

func TestMain(m *testing.M) {
	// Clean the gont networking state
	if err := g.CheckCaps(); err != nil {
		panic(err)
	}
	if err := g.TeardownAllNetworks(); err != nil {
		panic(err)
	}

	// Ensure the results directory exists
	if err := os.MkdirAll("./results", os.ModeDir); err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

// Download Speed

func testDlSuite(t *testing.T, suffix string, netem NetemConfig) {
	// Store our results
	name := fmt.Sprintf("./results/dl-speed-%s.json", suffix)
	speed := make(map[uint]map[uint][]time.Duration)

	// Create the progress so that the test
	// can be resumed if it crashes
	count := int64(0)
	p := newProgress(fmt.Sprintf("./results/dl-speed-%s.progress", suffix))

	// Load up the previously stored results
	// if we are resuming a previous test
	if p.task > 0 {
		f, err := os.Open(name)
		if err == nil {
			if err := json.NewDecoder(f).Decode(&speed); err != nil {
				panic(err)
			}
		}
		if err := f.Close(); err != nil {
			panic(err)
		}
	}

	// Run the test
	for _, fsize := range []uint{1, 5, 10, 50, 100, 250} {
		if speed[fsize] == nil {
			speed[fsize] = make(map[uint][]time.Duration)
		}
		for i := 0; i <= 14; i++ {
			for r := range 5 {
				if count < p.Load() {
					count += 1
					continue
				}

				d := testDlSpeed(uint(i), fsize*1024*1024, netem)
				speed[fsize][uint(i+1)] = append(speed[fsize][uint(i+1)], d)
				saveJSON(name, speed)
				t.Logf("fsize: %d, peers: %d, time: %v, run: %d\n", fsize, i+1, d, r+1)

				count += 1
				p.Set(count)
			}
		}
	}
}

func TestDlSpeed_Gigabit(t *testing.T) {
	testDlSuite(t, "gigabit", GigabitNetem)
}

func TestDlSpeed_WiFi(t *testing.T) {
	testDlSuite(t, "wifi", WifiNetem)
}

// Routing ability to avoid load

func TestRouting(t *testing.T) {
	l := sync.Mutex{}
	wg := sync.WaitGroup{}
	sem := make(chan struct{}, 5)

	load := make(map[uint][]RoutingResult)
	for _, fsize := range []uint{1, 5, 10, 50, 100, 250} {
		for r := range 5 {
			r := r
			wg.Add(1)

			sem <- struct{}{}
			go func() {
				defer func() {
					<-sem
					wg.Done()
				}()

				res := testRouting(fsize)

				l.Lock()
				load[fsize] = append(load[fsize], res)
				saveJSON("./results/routing-load.json", load)
				l.Unlock()

				t.Logf("fsize: %d, res: %+v, run: %d\n", fsize, res, r+1)
			}()
		}
	}

	wg.Wait()
}

// Churn resistance

func TestChurn_KVariance(t *testing.T) {
	l := sync.Mutex{}
	wg := sync.WaitGroup{}
	sem := make(chan struct{}, 3)
	results := make(map[int]map[int]ChurnResult)

	// Progress
	name := "./results/k-variance.json"
	count := int64(0)
	p := newProgress("./results/k-variance.progress")
	if p.task > 0 {
		f, err := os.Open(name)
		if err == nil {
			if err := json.NewDecoder(f).Decode(&results); err != nil {
				panic(err)
			}
		}
		if err := f.Close(); err != nil {
			panic(err)
		}
	}

	// Base test config
	ks := []int{1, 5, 10, 15, 20}
	meanSessionTimes := []int{200, 400, 600, 800, 1000, 2000, 3000, 4000}
	config := ChurnCtl{
		CIDR:           "10.42.0.0/16",
		Alpha:          1,
		AvgNodesOnline: 2500,
		SimLength:      2 * time.Hour,
	}

	for _, k := range ks {
		if results[k] == nil {
			results[k] = make(map[int]ChurnResult)
		}

		for _, mst := range meanSessionTimes {
			if count < p.Load() {
				count += 1
				continue
			}

			mst := mst
			wg.Add(1)

			sem <- struct{}{}
			go func() {
				defer func() {
					<-sem
					wg.Done()
				}()

				conf := config
				conf.K = k
				conf.MeanUptime = time.Duration(mst) * time.Second

				res := testChurn(conf)

				l.Lock()
				results[k][mst] = res
				saveJSON(name, results)
				l.Unlock()

				t.Logf("\rk = %d, mst = %d, r = %+v\n", k, mst, res)

				count += 1
				p.Set(count)
			}()
		}
	}

	wg.Wait()
}

func TestChurn_AlphaVariance(t *testing.T) {
	l := sync.Mutex{}
	wg := sync.WaitGroup{}
	sem := make(chan struct{}, 3)
	results := make(map[int]map[int]ChurnResult)

	// Progress
	name := "./results/alpha-variance.json"
	count := int64(0)
	p := newProgress("./results/alpha-variance.progress")
	if p.task > 0 {
		f, err := os.Open(name)
		if err == nil {
			if err := json.NewDecoder(f).Decode(&results); err != nil {
				panic(err)
			}
		}
		if err := f.Close(); err != nil {
			panic(err)
		}
	}

	// Base test config
	alphas := []int{1, 2, 3, 4, 5}
	meanSessionTimes := []int{200, 400, 600, 800, 1000, 2000, 3000, 4000}
	config := ChurnCtl{
		K:              20,
		CIDR:           "10.42.0.0/16",
		AvgNodesOnline: 2500,
		SimLength:      2 * time.Hour,
	}

	for _, alpha := range alphas {
		if results[alpha] == nil {
			results[alpha] = make(map[int]ChurnResult)
		}

		for _, mst := range meanSessionTimes {
			if count < p.Load() {
				count += 1
				continue
			}

			mst := mst
			wg.Add(1)

			sem <- struct{}{}
			go func() {
				defer func() {
					<-sem
					wg.Done()
				}()

				conf := config
				conf.Alpha = alpha
				conf.MeanUptime = time.Duration(mst) * time.Second

				res := testChurn(conf)

				l.Lock()
				results[alpha][mst] = res
				saveJSON(name, results)
				l.Unlock()

				t.Logf("\ralpha = %d, mst = %d, r = %+v\n", alpha, mst, res)

				count += 1
				p.Set(count)
			}()
		}
	}

	wg.Wait()
}

// Progress

type progress struct {
	sync.Mutex
	f    *os.File
	task int64
}

func newProgress(name string) *progress {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		panic(err)
	}

	t, err := io.ReadAll(f)
	if err != nil {
		panic(err)
	}
	if len(t) == 0 {
		return &progress{
			f:    f,
			task: 0,
		}
	}

	n, err := strconv.ParseInt(string(t), 10, 64)
	if err != nil {
		panic(err)
	}
	return &progress{
		f:    f,
		task: n,
	}
}

func (p *progress) Load() int64 {
	return p.task
}

func (p *progress) Set(n int64) {
	p.Lock()
	defer p.Unlock()

	p.task = n
	if err := p.f.Truncate(0); err != nil {
		panic(fmt.Errorf("could not write progress %d: %w", n, err))
	}
	if _, err := p.f.Seek(0, 0); err != nil {
		panic(err)
	}
	_, err := p.f.Write([]byte(fmt.Sprintf("%d", p.task)))
	if err != nil {
		panic(err)
	}
}

// JSON

func saveJSON(name string, v any) {
	f, err := os.Create(name)
	if err != nil {
		panic(err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
}

package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

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
	profiling := flag.Bool("profiling", false, "")
	testKVar := flag.Bool("k-var", false, "")
	testAlphaVar := flag.Bool("alpha-var", false, "")
	flag.Parse()

	if *profiling {
		go func() {
			fmt.Printf("pprof: %s\n", http.ListenAndServe("localhost:6060", nil))
		}()
	}

	// Churn parameters
	avgNodesOnline := 5000
	simLength := 2 * time.Hour
	meanSessionTimes := []int{200, 400, 600, 800, 1000, 2000, 3000, 4000}

	wg := sync.WaitGroup{}

	if *testKVar {
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := testKVariance([]int{1, 5, 10, 15, 20}, 1, meanSessionTimes, avgNodesOnline, simLength)
			if err != nil {
				panic(err)
			}
		}()
	}
	if *testAlphaVar {
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := testAlphaVariance([]int{1, 2, 3, 4, 5}, 20, meanSessionTimes, avgNodesOnline, simLength)
			if err != nil {
				panic(err)
			}
		}()
	}

	wg.Wait()
}

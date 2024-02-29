package main

import (
	"flag"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

func init() {
	// Turn off the default slog logger
	nilLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	slog.SetDefault(nilLogger)

	// Turn of chi logging
	middleware.DefaultLogger = func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}

func main() {
	testKVar := flag.Bool("k-var", false, "")
	testAlphaVar := flag.Bool("alpha-var", false, "")
	flag.Parse()

	// Churn parameters
	avgNodesOnline := 500
	simLength := 2 * time.Hour
	meanSessionTimes := []int{200, 400, 600, 800, 1000, 2000, 3000, 4000}

	if *testKVar {
		err := testKVariance([]int{1, 5, 10, 15, 20}, 1, meanSessionTimes, avgNodesOnline, simLength)
		if err != nil {
			panic(err)
		}
	}
	if *testAlphaVar {
		err := testAlphaVariance([]int{1, 2, 3, 4, 5}, 20, meanSessionTimes, avgNodesOnline, simLength)
		if err != nil {
			panic(err)
		}
	}
}

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/lmittmann/tint"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:               "inu",
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
}

func init() {
	rootCmd.AddCommand(dhtCmd)
	rootCmd.AddCommand(keyCmd)
	rootCmd.AddCommand(peerCmd)
}

func main() {
	slog.SetDefault(slog.New(
		tint.NewHandler(os.Stderr, &tint.Options{
			Level:      slog.LevelInfo,
			TimeFormat: time.DateTime,
		}),
	))
	middleware.DefaultLogger = httpLogger()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Signal parsing

func done() <-chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	return c
}

// Logging

func httpLogger() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			t1 := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				}

				attrs := []any{
					slog.Attr{Key: "url", Value: slog.StringValue(fmt.Sprintf("%s://%s%s", scheme, r.Host, r.RequestURI))},
					slog.Attr{Key: "method", Value: slog.StringValue(r.Method)},
					slog.Attr{Key: "ip", Value: slog.StringValue(r.RemoteAddr)},
					slog.Attr{Key: "elapsed", Value: slog.DurationValue(time.Since(t1).Round(time.Millisecond))},
				}

				slog.With(attrs...).Log(context.Background(), statusLevel(ww.Status()), fmt.Sprintf("%d", ww.Status()))
			}()

			next.ServeHTTP(ww, r)
		}
		return http.HandlerFunc(fn)
	}
}

func statusLevel(status int) slog.Level {
	switch {
	case status <= 0:
		return slog.LevelWarn
	case status < 400: // for codes in 100s, 200s, 300s
		return slog.LevelInfo
	case status >= 400 && status < 500:
		// switching to info level to be less noisy
		return slog.LevelInfo
	case status >= 500:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

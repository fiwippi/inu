package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"inu"
	"inu/cert"
	"inu/dht"
	"inu/fs"
	"inu/merkle"
	"inu/store"
)

const storePath = "inu.db"

// daemon

type daemon struct {
	// HTTP servers
	private *http.Server

	// Daemon state
	fs     *fs.FS
	store  *store.Store
	client *dht.Client

	// Config
	config dht.ClientConfig
}

func newDaemon(c dht.ClientConfig) *daemon {
	// Create the daemon
	s := store.NewStore(storePath)
	d := &daemon{
		store:  s,
		fs:     fs.FromStore(s),
		config: c,
		client: dht.NewClient(c),
	}

	// Register the HTTP handlers
	d.private = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", c.Port+1000),
		Handler: d.privateRouter(),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert.Cert()},
		},
	}

	return d
}

func (d *daemon) Start() {
	slog.Info("starting peer daemon",
		slog.Uint64("public", uint64(d.config.Port)),
		slog.String("private", d.private.Addr))

	go func() {
		if err := d.private.ListenAndServeTLS("", ""); err != nil {
			if err != http.ErrServerClosed {
				slog.Error("private execution failed", slog.Any("err", err))
				os.Exit(1)
			}
		}
	}()
}

func (d *daemon) Stop() error {
	defer slog.Info("peer daemon stopped")

	return d.private.Shutdown(context.Background())
}

func (d *daemon) privateRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	r.Post("/add", handleAdd(d))
	r.Post("/upload", handleUpload(d))

	return r
}

type addResp struct {
	Rs     []fs.Record `json:"rs"`
	ErrMsg string      `json:"err"`
}

func handleAdd(d *daemon) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path, err := io.ReadAll(r.Body)
		if err != nil || len(path) == 0 {
			slog.Error("could not decode path", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		_, rs, err := d.fs.AddPath(string(path))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(addResp{rs, err.Error()}); err != nil {
			slog.Error("failed to marshal add response", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

type uploadResp struct {
	Count  uint   `json:"count"`
	ErrMsg string `json:"err"`
}

func handleUpload(d *daemon) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		root, err := io.ReadAll(r.Body)
		if err != nil || len(root) == 0 {
			slog.Error("could not decode cid", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		count, err := func() (int, error) {
			cids, err := dag(inu.CID(root), d.store)
			if err != nil {
				return 0, err
			}

			for _, cid := range cids {
				k, err := dht.ParseCID(cid)
				if err != nil {
					return 0, err
				}
				if err := d.client.PutKey(k); err != nil {
					return 0, err
				}
			}
			return len(cids), nil
		}()

		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(uploadResp{uint(count), errMsg}); err != nil {
			slog.Error("failed to marshal upload response", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// daemon API

type daemonAPI struct {
	port   uint16
	client *http.Client
}

func newDaemonAPI(c dht.ClientConfig) *daemonAPI {
	return &daemonAPI{
		port:   c.Port + 1000,
		client: cert.Client(10 * time.Second),
	}
}

func (api *daemonAPI) offline() (bool, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", api.port))
	if err == nil {
		if err := ln.Close(); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (api *daemonAPI) add(path string) ([]fs.Record, error) {
	url := fmt.Sprintf("https://127.0.0.1:%d/add", api.port)
	resp, err := api.client.Post(url, "application/json", strings.NewReader(path))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, badCodeError(resp.StatusCode)
	}

	// Decode the reply and ensure it's not errored
	var add addResp
	if err := json.NewDecoder(resp.Body).Decode(&add); err != nil {
		return nil, err
	}
	if add.ErrMsg != "" {
		return nil, fmt.Errorf(add.ErrMsg)
	}

	return add.Rs, nil
}

func (api *daemonAPI) upload(root inu.CID) (uint, error) {
	url := fmt.Sprintf("https://127.0.0.1:%d/upload", api.port)
	resp, err := api.client.Post(url, "application/json", strings.NewReader(string(root)))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, badCodeError(resp.StatusCode)
	}

	// Decode the reply and ensure it's not errored
	var upload uploadResp
	if err := json.NewDecoder(resp.Body).Decode(&upload); err != nil {
		return 0, err
	}
	if upload.ErrMsg != "" {
		return 0, fmt.Errorf(upload.ErrMsg)
	}

	return upload.Count, nil
}

func badCodeError(c int) error {
	return fmt.Errorf("bad response code: %d", c)
}

// Helpers

func dag(cid inu.CID, s *store.Store) ([]inu.CID, error) {
	b, err := s.Get(cid)
	if err != nil {
		return nil, err
	}
	m, err := merkle.ParseBlock(b)
	if err != nil {
		return nil, err
	}

	cids := []inu.CID{cid}
	for _, l := range m.Links() {
		cs, err := dag(l.CID, s)
		if err != nil {
			return nil, err
		}
		cids = append(cids, cs...)
	}

	return cids, nil
}

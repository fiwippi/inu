package inu

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"inu/cert"
	"inu/cid"
	"inu/dht"
	"inu/fs"
	"inu/merkle"
	"inu/store"
)

// daemon

type Daemon struct {
	// HTTP/RPC state
	public   *http.Server
	privateL net.Listener
	privateH *rpc.Server

	// Daemon state
	fs    *fs.FS
	store *store.Store
	dht   *dht.Client
	http  *http.Client

	// Config
	config dht.ClientConfig
}

func NewDaemon(storePath string, c dht.ClientConfig) *Daemon {
	// Create the daemon
	s := store.NewStore(storePath)
	d := &Daemon{
		privateH: rpc.NewServer(),
		store:    s,
		fs:       fs.FromStore(s),
		config:   c,
		dht:      dht.NewClient(c),
		http:     cert.Client(10 * time.Second),
	}

	// Register the HTTP handler
	d.public = &http.Server{
		Addr:      fmt.Sprintf("0.0.0.0:%d", c.Port),
		Handler:   d.router(),
		TLSConfig: cert.Config(),
	}

	// Register the RPC service
	if err := d.privateH.Register(d); err != nil {
		panic(err)
	}

	return d
}

func (d *Daemon) Start() {
	slog.Info("Starting peer daemon",
		slog.String("public", d.public.Addr),
		slog.String("private", d.privateAddress()))

	go func() {
		if err := d.public.ListenAndServeTLS("", ""); err != nil {
			if err != http.ErrServerClosed {
				slog.Error("Public execution failed", slog.Any("err", err))
				os.Exit(1)
			}
		}
	}()

	go func() {
		listener, err := tls.Listen("tcp", d.privateAddress(), cert.Config())
		if err != nil {
			slog.Error("RPC listen failed", slog.Any("err", err))
			os.Exit(1)
		}
		d.privateL = listener

		for {
			conn, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					slog.Error("Failed to accept incoming RPC", slog.Any("err", err))
				}
				break
			}
			slog.Info("Accepted RPC", slog.Any("address", conn.RemoteAddr()))

			go func() {
				defer conn.Close()
				d.privateH.ServeConn(conn)
			}()
		}
	}()
}

func (d *Daemon) Stop() error {
	defer slog.Info("Peer daemon stopped")

	ctx := context.Background()
	if err := d.public.Shutdown(ctx); err != nil {
		return err
	}
	return d.privateL.Close()
}

// Router (public)

func (d *Daemon) router() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	r.Get("/block/{cid}", func(w http.ResponseWriter, r *http.Request) {
		b, err := d.store.Get(cid.CID(chi.URLParam(r, "cid")))
		if err != nil {
			slog.Error("Could not get block", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(b); err != nil {
			slog.Error("Failed to marshal block", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
		}

	})

	return r
}

// RPC (private)

type AddReply struct {
	Records []fs.Record
}

func (d *Daemon) AddPath(path string, reply *AddReply) error {
	_, rs, err := d.fs.AddPath(path)
	if err != nil {
		return err
	}

	reply.Records = rs
	return nil
}

type CatReply struct {
	Data []byte
}

func (d *Daemon) Cat(path string, reply *CatReply) error {
	n, err := d.fs.ResolvePath(path)
	if err != nil {
		return err
	}

	data, err := d.fs.ReadBytes(&n)
	if err != nil {
		return err
	}

	reply.Data = data
	return nil
}

func (d *Daemon) Upload(path string, count *uint) error {
	n, err := d.fs.ResolvePath(path)
	if err != nil {
		return err
	}

	cids, err := d.dag(n.Block().CID)
	if err != nil {
		return err
	}

	for _, cid := range cids {
		k, err := dht.ParseCID(cid)
		if err != nil {
			return err
		}
		if err := d.dht.PutKey(k); err != nil {
			return err
		}
	}

	*count = uint(len(cids))
	return nil
}

func (d *Daemon) Download(cid cid.CID, reply *struct{}) error {
	k, err := dht.ParseCID(cid)
	if err != nil {
		return err
	}
	return d.download(k)
}

func (d *Daemon) download(k dht.Key) error {
	// To download a Merkle DAG
	//   1. Get peers for key
	//   2. Download block represented by key from peer
	//   3. Ensure valid block is returned
	//   4. Parse block as Merkle node
	//   5. Add the block to the store
	//   6. Download blocks denoted in Merkle links

	// 1
	peers, err := d.dht.FindPeers(k)
	if err != nil {
		return err
	}

	// 2
	p := peers[0]
	resp, err := d.http.Get(fmt.Sprintf("https://%s:%d/block/%s", p.IP, p.Port, k.MarshalB32()))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return badCodeError(resp.StatusCode)
	}

	// 3
	b := store.Block{}
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		return err
	}
	if !b.Valid() {
		return fmt.Errorf("invalid block")
	}

	// 4
	m, err := merkle.ParseBlock(b)
	if err != nil {
		return err
	}

	// 5
	if err := d.store.Put(b); err != nil {
		return err
	}

	// 6
	for _, l := range m.Links() {
		kk, err := dht.ParseCID(l.CID)
		if err != nil {
			return err
		}

		if err := d.download(kk); err != nil {
			return err
		}
	}

	return nil
}

// Helpers

func (d *Daemon) dag(id cid.CID) ([]cid.CID, error) {
	b, err := d.store.Get(id)
	if err != nil {
		return nil, err
	}
	m, err := merkle.ParseBlock(b)
	if err != nil {
		return nil, err
	}

	cids := []cid.CID{id}
	for _, l := range m.Links() {
		cs, err := d.dag(l.CID)
		if err != nil {
			return nil, err
		}
		cids = append(cids, cs...)
	}

	return cids, nil
}

func (d *Daemon) privateAddress() string {
	return fmt.Sprintf("127.0.0.1:%d", d.config.Port+1000)
}

func badCodeError(c int) error {
	return fmt.Errorf("bad response code: %d", c)
}

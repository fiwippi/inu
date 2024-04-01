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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/sync/errgroup"

	"inu/cert"
	"inu/cid"
	"inu/dht"
	"inu/fs"
	"inu/merkle"
	"inu/store"
)

// Daemon Config

type DaemonConfig struct {
	dht.ClientConfig

	StorePath string
	Host      string
	PublicIP  string // The daemon is not necessarily hosted on the same IP that it's accessible at
	RpcPort   uint16
}

func DefaultDaemonConfig() DaemonConfig {
	def := dht.DefaultClientConfig()

	return DaemonConfig{
		ClientConfig: dht.DefaultClientConfig(),
		StorePath:    "inu.db",
		Host:         "0.0.0.0",
		RpcPort:      def.Port + 1000,
	}
}

// Daemon

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
	sem   chan struct{} // Semaphores for controlling download concurrency

	// Config
	config DaemonConfig
}

func NewDaemon(config DaemonConfig) *Daemon {
	if config.PublicIP == "" {
		panic("must set public IP, even it it's equivalent to the host")
	}

	s := store.NewStore(config.StorePath)
	d := &Daemon{
		privateH: rpc.NewServer(),
		store:    s,
		fs:       fs.FromStore(s),
		config:   config,
		dht:      dht.NewClient(config.ClientConfig),
		http:     cert.Client(),
		sem:      make(chan struct{}, 5000), // Max concurrent downloads of 5000 blocks
	}

	// Register the HTTP handler
	d.public = &http.Server{
		Addr:      fmt.Sprintf("%s:%d", config.Host, config.Port),
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
	privateAddr := fmt.Sprintf("%s:%d", d.config.Host, d.config.RpcPort)

	slog.Info("Starting peer daemon",
		slog.String("public", d.public.Addr),
		slog.String("private", privateAddr))

	publicL, err := net.Listen("tcp", d.public.Addr)
	if err != nil {
		slog.Error("Public listen failed", slog.Any("err", err))
		os.Exit(1)
	}

	go func() {
		if err := d.public.ServeTLS(publicL, "", ""); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				slog.Error("Public execution failed", slog.Any("err", err))
				os.Exit(1)
			}
		}
	}()

	privateL, err := tls.Listen("tcp", privateAddr, cert.Config())
	if err != nil {
		slog.Error("RPC listen failed", slog.Any("err", err))
		os.Exit(1)
	}

	go func() {
		d.privateL = privateL

		for {
			conn, err := d.privateL.Accept()
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
	if err := d.store.Close(); err != nil {
		return err
	}
	ctx := context.Background()
	if err := d.public.Shutdown(ctx); err != nil {
		return err
	}

	defer slog.Info("Peer daemon stopped")
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

type AddBytesReply struct {
	CID cid.CID
}

func (d *Daemon) AddBytes(bytes []byte, reply *AddBytesReply) error {
	n, err := d.fs.AddBytes(bytes)
	if err != nil {
		return err
	}

	reply.CID = n.Block().CID
	return nil
}

type AddPathReply struct {
	Records []fs.Record
}

func (d *Daemon) AddPath(path string, reply *AddPathReply) error {
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

	cids, _, err := d.fs.DAG(n.Block().CID)
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

func (d *Daemon) Download(cid cid.CID, _ *struct{}) error {
	k, err := dht.ParseCID(cid)
	if err != nil {
		return err
	}

	eg, _ := errgroup.WithContext(context.Background())
	eg.Go(func() error {
		return d.download(eg, k)
	})
	if err := eg.Wait(); err != nil {
		return err
	}
	return eg.Wait()
}

func (d *Daemon) download(eg *errgroup.Group, k dht.Key) error {
	// Semaphores ensure a max limit on concurrent downloads
	d.sem <- struct{}{}
	defer func() {
		<-d.sem
	}()

	// To download a Merkle DAG
	//   1. Get peers for key
	//   2. Download block represented by key from a peer
	//      which is not ourselves
	//   3. Ensure valid block is returned
	//   4. Parse block as Merkle node
	//   5. Add the block to the store
	//      (Important we do this before we announce we
	//      own the block!)
	//   6. Notify the DHT that we own the block
	//   7. Begin downloading blocks denoted in Merkle links

	// 1
	peers, err := d.dht.FindPeers(k)
	if err != nil {
		return err
	}

	// 2
	i := index(peers, d.config.PublicIP)
	if i != -1 {
		peers = append(peers[:i], peers[i+1:]...)
	}
	if len(peers) == 0 {
		return fmt.Errorf("no peers have the key")
	}
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
	if err := d.dht.PutKey(k); err != nil {
		return err
	}

	// 7
	for _, l := range m.Links() {
		eg.Go(func() error {
			kk, err := dht.ParseCID(l.CID)
			if err != nil {
				return err
			}

			return d.download(eg, kk)
		})
	}

	return nil
}

// Helpers

func badCodeError(c int) error {
	return fmt.Errorf("bad response code: %d", c)
}

func index(ps []dht.Peer, p string) int {
	for i := range ps {
		if p == ps[i].IP.String() {
			return i
		}
	}
	return -1
}

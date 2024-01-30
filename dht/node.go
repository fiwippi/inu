package dht

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"inu/cert"
)

// Node Config

type NodeConfig struct {
	// HTTP
	Host string `json:"host"`
	Port uint16 `json:"port"`

	// Kademlia settings
	ID           Key           `json:"id"`
	Alpha        int           `json:"alpha"` // Degree of parallelism in requests
	K            int           `json:"k"`     // Size of each bucket
	RepublishDur time.Duration `json:"republish_duration"`
	RefreshDur   time.Duration `json:"refresh_duration"`
	Expiry       time.Duration `json:"expiry_duration"`

	// Auth
	SwarmKey  Key `json:"swarm_key"`  // Used for inter-node RPCs
	UploadKey Key `json:"upload_key"` // Used for peer-node RPCs
}

func DefaultNodeConfig() NodeConfig {
	return NodeConfig{
		Host:         "localhost",
		Port:         3000,
		ID:           Key{},
		Alpha:        3,
		K:            20,
		RepublishDur: time.Hour,
		RefreshDur:   time.Hour,
		Expiry:       24 * time.Hour,
		SwarmKey:     Key{},
		UploadKey:    Key{},
	}
}

// Node

type Node struct {
	server *http.Server

	// Task accounting
	stopRefresh   chan struct{}
	stopRepublish chan struct{}

	// Kademlia data
	self      Contact       // The node's own contact info
	rpc       rpc           // RPC used for transport
	rt        *routingTable // Routing table for the DHT
	peerStore *peerStore

	// ASN routing data
	asnTrie *trieNode
	asnMut  sync.RWMutex

	// Node/Kademlia config
	config NodeConfig
}

func NewNode(config NodeConfig) (*Node, error) {
	// Create the self contact
	self := Contact{
		ID:      config.ID,
		Address: fmt.Sprintf("%s:%d", config.Host, config.Port),
	}

	// Create the node
	n := &Node{
		self:          self,
		rpc:           newRpc(self, config.SwarmKey),
		rt:            newRoutingTable(self, config.K),
		peerStore:     newPeerStore(config.Expiry),
		asnTrie:       new(trieNode),
		config:        config,
		stopRefresh:   make(chan struct{}),
		stopRepublish: make(chan struct{}),
	}

	// Create the HTTP server the node serves data from
	n.server = &http.Server{
		Addr:    n.self.Address,
		Handler: n.router(),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert.Cert()},
		},
	}

	return n, nil
}

func (n *Node) Start() {
	slog.Info("starting DHT node", slog.Any("id", n.self.ID), slog.String("address", n.self.Address))

	// Start the server
	go func() {
		if err := n.server.ListenAndServeTLS("", ""); err != nil {
			if err != http.ErrServerClosed {
				slog.Error("node execution failed", slog.Any("err", err))
				os.Exit(1)
			}
		}
	}()

	// Start each long-running task
	go n.refresh()
	go n.republish()
}

func (n *Node) Stop() error {
	defer slog.Info("DHT stopped")

	// Stop long-running tasks
	n.stopRefresh <- struct{}{}
	n.stopRepublish <- struct{}{}

	// Stop the server
	return n.server.Shutdown(context.Background())
}

// Router

func (n *Node) router() *chi.Mux {
	// Create the router
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	// Register the public routes
	r.Get("/key/{key}", handleKeyGet(n))
	r.With(validateUpload(n)).Put("/key/{key}", handleKeyPut(n))

	// Register the RPC routes, these require that
	//   1. Requests have a valid swarm key (Inu-Swarm)
	//   2. Requests have a valid source set (Inu-Src)
	r.Route("/rpc", func(r chi.Router) {
		r.Use(authSwarmKey(n))
		r.Use(parseSrc(n))

		r.Get("/ping", handlePing(n))
		r.Post("/store", handleStore(n))
		r.Get("/find-node/{key}", handleFindNode(n))
		r.Get("/find-peers/{key}", handleFindPeers(n))
	})

	return r
}

// Routines

func (n *Node) Bootstrap(c Contact) error {
	// Update the node in our routing table
	n.updateContact(c)

	// Find all nodes closest to ourselves
	cs, err := n.FindNode(n.self.ID)
	if err != nil {
		return err
	}

	// Add these contacts back to our routing table
	for _, c := range cs {
		n.updateContact(c)
	}

	return nil
}

func (n *Node) refresh() {
	t := time.NewTicker(n.config.RefreshDur)

	for {
		select {
		case <-t.C:
			slog.Info("refreshing buckets")

			// Pick an ID from each bucket
			cs := make([]Contact, 0)
			n.rt.Lock()
			for _, b := range n.rt.buckets {
				x := time.Since(b.lastUpdated) > time.Hour
				y := b.contacts.Len() > 0
				if x && y {
					cs = append(cs, b.contacts.Front().Value.(Contact))
				}
			}
			n.rt.Unlock()

			// Lookup each ID on the network and refresh
			// its bucket with any nodes found
			for _, c := range cs {
				newContacts, err := n.FindNode(c.ID)
				if err != nil {
					slog.Error("failed to refresh bucket",
						slog.Any("contact", c), slog.Any("err", err))
				}

				for _, newC := range newContacts {
					n.updateContact(newC)
				}
			}
		case <-n.stopRefresh:
			slog.Info("done refreshing")
			return
		}
	}
}

func (n *Node) republish() {
	t := time.NewTicker(n.config.RepublishDur)

	for {
		select {
		case <-t.C:
			slog.Info("republishing keys")

			ps := n.peerStore.GetAll()
			for _, p := range ps {
				if err := n.Store(p.K, p.P); err != nil {
					slog.Error("failed to republish peers", slog.Any("key", p.K),
						slog.Any("peers", p.P), slog.Any("err", err))
				}
			}
		case <-n.stopRepublish:
			slog.Info("done republishing")
			return
		}
	}
}

// Middleware

const (
	ctxKey ctxKeyType = iota
	ctxPort
	ctxIP
	ctxSrc
)

type ctxKeyType int

func validateUpload(n *Node) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Before we handle the request we must parse
			// different values from the URL and body
			//   1. The target key
			//   2. The port number
			//   3. The incoming IP address

			// 1
			k := Key{}
			err := k.UnmarshalB32(chi.URLParam(r, "key"))
			if err != nil {
				slog.Error("could not parse key", slog.Any("err", err))
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			// 2
			var port uint16
			err = json.NewDecoder(r.Body).Decode(&port)
			if err != nil {
				slog.Error("could not decode put request", slog.Any("err", err))
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			// 3
			ip, err := parseIP(r.RemoteAddr)
			if err != nil {
				slog.Error("remote address invalid", slog.Any("err", err))
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			ctx := context.WithValue(r.Context(), ctxKey, k)
			ctx = context.WithValue(ctx, ctxPort, port)
			ctx = context.WithValue(ctx, ctxIP, ip)
			r = r.WithContext(ctx)

			// If we are not protected by an upload key then
			// we accept any well-formed PUT
			if n.config.UploadKey == (Key{}) {
				next.ServeHTTP(w, r)
				return
			}

			// Parse upload key (if applicable)
			// and serve the PUT if it's valid
			inuUpload := r.Header.Get("Inu-Upload")
			if inuUpload != "" {
				uploadKey := Key{}
				err := uploadKey.UnmarshalB32(inuUpload)
				if err != nil {
					slog.Error("could not parse upload key in header", slog.Any("err", err))
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				if uploadKey == n.config.UploadKey {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Otherwise we verify that the key already exists
			// somewhere on the network, find peers checks our
			// own store for us first
			_, err = n.FindPeers(k)
			if err == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Otherwise we have an invalid upload request
			slog.Error("invalid upload request",
				slog.Any("key", k), slog.Any("err", err))
			w.WriteHeader(http.StatusUnauthorized)
		})
	}
}

func authSwarmKey(n *Node) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Parse swarm key
			k := Key{}
			err := k.UnmarshalB32(r.Header.Get("Inu-Swarm"))
			if err != nil {
				slog.Error("could not parse swarm key in header", slog.Any("err", err))
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			// Validate the swarm key
			if k != n.config.SwarmKey {
				slog.Error("invalid swarm key", slog.Any("key", k))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			// Serve the next request
			next.ServeHTTP(w, r)
		})
	}
}

func parseSrc(n *Node) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Parse src from header
			c := Contact{}
			err := json.Unmarshal([]byte(r.Header.Get("Inu-Src")), &c)
			if err != nil {
				slog.Error("could not parse src in header", slog.Any("err", err))
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			// Update the routing table
			n.updateContact(c)

			// Add the source to the context
			// and serve the next request with
			// the new context
			ctx := context.WithValue(r.Context(), ctxSrc, c)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Handlers

func handleKeyGet(n *Node) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		k := Key{}
		err := k.UnmarshalB32(chi.URLParam(r, "key"))
		if err != nil {
			slog.Error("could not parse key", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Get the ASN of the requesting IP
		ip, err := parseIP(r.RemoteAddr)
		if err != nil {
			slog.Error("remote address invalid", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n.asnMut.RLock()
		asn, _ := n.asnTrie.Match(ip)
		n.asnMut.RUnlock()

		// Query the peers from the store/dht and return
		// a max of K peers, we return peers which are
		// in the same ASN as the requesting IP first
		p, err := n.FindPeers(k)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		slices.SortStableFunc(p, func(a, b Peer) int {
			if asn == a.ASN {
				return -1
			}
			return 1
		})
		if len(p) > n.config.K {
			p = p[:n.config.K]
		}

		// Reply with the peer
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(p); err != nil {
			slog.Error("failed to marshal peers", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

func handleKeyPut(n *Node) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		k := r.Context().Value(ctxKey).(Key)
		port := r.Context().Value(ctxPort).(uint16)
		ip := r.Context().Value(ctxIP).(netip.Addr)

		n.asnMut.RLock()
		asn, _ := n.asnTrie.Match(ip)
		n.asnMut.RUnlock()

		// Generate the peer info
		p := Peer{
			IP:        ip,
			Port:      port,
			ASN:       asn,
			Published: time.Now().UTC(),
		}

		if err := n.Store(k, []Peer{p}); err != nil {
			slog.Error("could not store pair", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

func handlePing(_ *Node) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Do nothing, updating the routing table is
		// taken care of by the middleware
		w.WriteHeader(http.StatusOK)
	}
}

func handleStore(n *Node) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := pair{}
		err := json.NewDecoder(r.Body).Decode(&p)
		if err != nil {
			slog.Error("could not decode pair", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Store the Pair
		n.peerStore.Put(p.K, p.P)

		w.WriteHeader(http.StatusOK)
	}
}

func handleFindNode(n *Node) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		k := Key{}
		err := k.UnmarshalB32(chi.URLParam(r, "key"))
		if err != nil {
			slog.Error("could not parse key", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Find the closest node
		src := r.Context().Value(ctxSrc).(Contact)
		closest := n.rt.FindClosestNodes(&k, &src.ID)

		// Reply with the closest contacts
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(closest); err != nil {
			slog.Error("failed to marshal closest contacts", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

func handleFindPeers(n *Node) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		k := Key{}
		err := k.UnmarshalB32(chi.URLParam(r, "key"))
		if err != nil {
			slog.Error("could not parse key", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		resp := findPeersResp{}

		// Query the peers from the store
		ps, err := n.peerStore.Get(k)
		if err == nil {
			// On success return the peers
			resp.Found = true
			resp.Peers = ps
		} else {
			// Otherwise return the closest nodes
			// to the peer set
			src := r.Context().Value(ctxSrc).(Contact)
			resp.Contacts = n.rt.FindClosestNodes(&k, &src.ID)
		}

		// Reply with the closest contacts
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("failed to marshal find peers response", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// ASN trie

func (n *Node) UpdateASN(p netip.Prefix, asn int) error {
	n.asnMut.Lock()
	defer n.asnMut.Unlock()

	return n.asnTrie.Insert(p, asn)
}

// Helpers

func (n *Node) updateContact(c Contact) {
	n.rt.UpdateContact(c, n.rpc)
}

func parseIP(s string) (netip.Addr, error) {
	ip, err := netip.ParseAddr(s)
	if err == nil && ip.Is4() {
		return ip, nil
	}
	ipp, err := netip.ParseAddrPort(s)
	if err == nil && ipp.Addr().Is4() {
		return ipp.Addr(), nil
	}

	return netip.Addr{}, fmt.Errorf("invalid ip")
}

// Lookups

const (
	findNodeLookup lookupKind = iota
	findPeersLookup
)

type lookupKind int

func (n *Node) lookup(k Key, lk lookupKind) (findPeersResp, error) {
	// Initialise the list of nodes closest to our target
	closest := newShortlist(k, n.config.K)

	// Assign the alpha closest nodes from the routing
	// to this list
	initial := n.rt.FindClosestNodes(&k, &n.self.ID)
	if len(initial) == 0 {
		return findPeersResp{}, fmt.Errorf("no close nodes")
	}
	if len(initial) > n.config.Alpha {
		initial = initial[:n.config.Alpha]
	}
	closest.Insert(initial...)

	// We continue querying the contacts closest to our target
	// to find closer contacts until either:
	//   1. We are unable to find a contact closer than the
	//      one found in the last round of querying
	//   2. All the contacts that we have found have been
	//      queried

	limit := n.config.Alpha
	peers := atomic.Value{}
	closestNode := closest.list[0]

	for {
		// Pick nodes we haven't already contacted
		toContact := closest.PickNotContacted(limit)

		// Contact each of these nodes in parallel
		var wg sync.WaitGroup

		for _, c := range toContact {
			c := c

			// Mark the node as contacted (otherwise it's
			// possible we could send two queries to the
			// same node if one is taking excessively long
			// to reply)
			closest.SetContacted(c)

			wg.Add(1)
			go func() {
				defer wg.Done()

				done := make(chan struct{})
				go func() {
					defer close(done)

					// Send the RPC
					switch lk {
					case findNodeLookup:
						cs, err := n.rpc.FindNode(c, k)
						if err != nil {
							// Unreachable contacts are always removed from consideration
							slog.Error("unreachable node", slog.Any("contact", c), slog.Any("err", err))
							closest.Remove(c)

							// Notify the checker thread that we can exit
							// (we send with select in case the checker is done)
							select {
							case done <- struct{}{}:
							default:
							}

							return
						}

						// Add the contacts for consideration as closest to the target
						closest.Insert(cs...)
					case findPeersLookup:
						resp, err := n.rpc.FindPeers(c, k)
						if err != nil {
							// Unreachable contacts are always removed from consideration
							slog.Error("unreachable node", slog.Any("contact", c), slog.Any("err", err))
							closest.Remove(c)

							// Notify the checker thread that we can exit
							// (we send with select in case the checker is done)
							select {
							case done <- struct{}{}:
							default:
							}

							return
						}

						// Add the contacts for consideration as closest to the target
						if !resp.Found {
							closest.Insert(resp.Contacts...)
						} else {
							// Otherwise we set the found value
							peers.Store(resp.Peers)
							closest.SetAsHavingPeers(c)
						}
					}

					// Notify that we are done, skip if done is closed already
					select {
					case done <- struct{}{}:
					default:
						// done is ignored if we take too long to reply,
						// but since we have a successful reply we add
						// ourselves back into consideration
						closest.Insert(c)
					}
				}()

				// If we are unable to reach a contact in time
				// then we remove it from consideration until
				// we receive a reply from it
				select {
				case <-time.After(5 * time.Second):
					slog.Warn("node timed out", slog.Any("contact", c))
					closest.Remove(c)
				case <-done:
				}
			}()
		}

		// Wait for the parallel requests to either
		// finish successfully or time out
		wg.Wait()

		// If we've found a value then we return instantly
		p := peers.Load()
		if p != nil {
			p := p.([]Peer)

			// Store our peer set at the closest node to the target
			// that we have seen which did not have the set (caching)
			closestWithout, err := closest.PickClosestWithoutValue()
			if err == nil {
				err := n.rpc.Store(closestWithout, k, p)
				if err != nil {
					slog.Error("failed to cache pair", slog.Any("dst", closestWithout), slog.Any("err", err))
				}
			}

			return findPeersResp{
				Found: true,
				Peers: p,
			}, nil
		}

		// If our closest contacts list is empty that means
		// all contacts failed to reply in time, so we exit
		if closest.Len() == 0 {
			return findPeersResp{}, fmt.Errorf("all contacts unreachable")
		}

		// If we have a new closest node then we continue
		// another round of querying another alpha nodes
		h := closest.Head()
		if closestNode != h {
			closestNode = h
			limit = n.config.Alpha
			continue
		}

		// If we have queried all our contacts then
		// we can return the closest contacts
		if closest.AllContacted() {
			// We return a deep copy since it's
			// possible that unfinished queries
			// could modify the slice after it's
			// returned
			return findPeersResp{
				Found:    false,
				Contacts: closest.DeepCopy(),
			}, nil
		}

		// Otherwise we query all remaining nodes instead
		// of just up to alpha nodes
		limit = math.MaxInt
	}
}

func (n *Node) Store(k Key, p []Peer) error {
	// Store the pair in our own store
	n.peerStore.Put(k, p)

	// Find nodes to store the value in
	cs, err := n.FindNode(k)
	if err != nil {
		return err
	}

	// Send the store RPC to each contact
	for _, c := range cs {
		err := n.rpc.Store(c, k, p)
		if err != nil {
			return err
		}
	}

	return nil
}

func (n *Node) FindNode(k Key) ([]Contact, error) {
	resp, err := n.lookup(k, findNodeLookup)
	if err != nil {
		return nil, err
	}
	if resp.Found {
		return nil, fmt.Errorf("could not find node with key")
	}
	return resp.Contacts, nil
}

func (n *Node) FindPeers(k Key) ([]Peer, error) {
	// Check our own store first
	v, err := n.peerStore.Get(k)
	if err == nil {
		return v, nil
	}

	// Otherwise we ask on the network
	resp, err := n.lookup(k, findPeersLookup)
	if err != nil {
		return nil, err
	}
	if !resp.Found {
		return nil, fmt.Errorf("could not find peers who provide key")
	}
	return resp.Peers, nil
}

// Shortlist

type shortlist struct {
	sync.Mutex

	k         int
	target    Key
	list      []Contact
	contacted map[Key]struct{}
	hasPeers  map[Key]struct{}
}

func newShortlist(target Key, k int) *shortlist {
	return &shortlist{
		k:         k,
		target:    target,
		list:      make([]Contact, 0),
		contacted: make(map[Key]struct{}),
		hasPeers:  make(map[Key]struct{}),
	}
}

func (s *shortlist) search(c Contact) int {
	return sort.Search(len(s.list), func(i int) bool {
		d1 := s.target.xor(c.ID)
		d2 := s.target.xor(s.list[i].ID)
		r := d2.cmp(d1)
		return r == 0 || r == 1 // d2 >= d1
	})
}

func (s *shortlist) Len() int {
	s.Lock()
	defer s.Unlock()

	return len(s.list)
}

func (s *shortlist) Head() Contact {
	s.Lock()
	defer s.Unlock()

	return s.list[0]
}

func (s *shortlist) Insert(cs ...Contact) {
	s.Lock()
	defer s.Unlock()

	for _, c := range cs {
		// Add in sorted position if not already exists
		i := s.search(c)
		if i < len(s.list) && s.list[i] == c {
			continue
		}
		s.list = append(s.list, Contact{})
		copy(s.list[i+1:], s.list[i:])
		s.list[i] = c
	}

	// Shrink list if too large
	if len(s.list) > s.k {
		s.list = s.list[:s.k]
	}
}

func (s *shortlist) Remove(cs ...Contact) {
	s.Lock()
	defer s.Unlock()

	for _, c := range cs {
		// Remove from the list
		i := s.search(c)
		if i < len(s.list) && s.list[i] == c {
			s.list = append(s.list[:i], s.list[i+1:]...)
		}

		// Remove from the contacted map
		delete(s.contacted, c.ID)
	}
}

func (s *shortlist) SetContacted(cs ...Contact) {
	s.Lock()
	defer s.Unlock()

	for _, c := range cs {
		s.contacted[c.ID] = struct{}{}
	}
}

func (s *shortlist) SetAsHavingPeers(cs ...Contact) {
	s.Lock()
	defer s.Unlock()

	for _, c := range cs {
		s.hasPeers[c.ID] = struct{}{}
	}
}

func (s *shortlist) AllContacted() bool {
	s.Lock()
	defer s.Unlock()

	for _, c := range s.list {
		if _, found := s.contacted[c.ID]; !found {
			return false
		}
	}
	return true
}

func (s *shortlist) PickNotContacted(limit int) []Contact {
	s.Lock()
	defer s.Unlock()

	toContact := make([]Contact, 0)
	for _, c := range s.list {
		if len(toContact) >= limit {
			break
		}
		if _, found := s.contacted[c.ID]; found {
			continue
		}

		toContact = append(toContact, c)
	}
	return toContact
}

func (s *shortlist) PickClosestWithoutValue() (Contact, error) {
	s.Lock()
	defer s.Unlock()

	for _, c := range s.list {
		if _, found := s.hasPeers[c.ID]; !found {
			return c, nil
		}
	}

	return Contact{}, fmt.Errorf("all nodes have the value")
}

func (s *shortlist) DeepCopy() []Contact {
	s.Lock()
	defer s.Unlock()

	dst := make([]Contact, len(s.list))
	copy(dst, s.list)
	return dst
}

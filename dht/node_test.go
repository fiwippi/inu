package dht

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inu/cert"
)

// Node Config

func TestNodeConfig_JSON(t *testing.T) {
	c1 := DefaultNodeConfig()
	data, err := json.MarshalIndent(c1, "", " ")
	require.NoError(t, err)
	fmt.Println(string(data))

	var c2 NodeConfig
	require.NoError(t, json.Unmarshal(data, &c2))
	require.Equal(t, c1, c2)
}

// Nodes

func TestNode_AltHost(t *testing.T) {
	t.Run("no alt host", func(t *testing.T) {
		n, err := NewNode(DefaultNodeConfig())
		require.NoError(t, err)
		require.Equal(t, Contact{Address: "0.0.0.0:3000"}, n.self)
	})

	t.Run("alt host", func(t *testing.T) {
		c := DefaultNodeConfig()
		c.AltHost = "alt"
		n, err := NewNode(c)
		require.NoError(t, err)
		require.Equal(t, Contact{Address: "alt:3000"}, n.self)
	})
}

func TestNode_Bootstrap(t *testing.T) {
	n := newTestNode(0, "3000")
	bootstrapNode := Contact{ID: ParseUint64(1), Address: "3001"} // Node we boostrap with
	extraNode := Contact{ID: ParseUint64(2), Address: "3002"}     // Node which the bootstrap node knows

	reqNum := 0
	n.rpc = &mockLookupRpc{
		fn: func(dst Contact, k Key) ([]Contact, error) {
			defer func() {
				reqNum += 1
			}()

			// Bootstrap should query the bootstrap
			// node with our own key and then query
			// the returned node
			switch reqNum {
			case 0:
				require.Equal(t, bootstrapNode, dst)
				require.Equal(t, n.self.ID, k)
				return []Contact{extraNode}, nil
			case 1:
				require.Equal(t, extraNode, dst)
				require.Equal(t, n.self.ID, k)
				return []Contact{bootstrapNode}, nil
			}

			return nil, fmt.Errorf("bad rpc")
		},
	}

	require.NoError(t, n.Bootstrap(bootstrapNode))
	require.Equal(t, 2, reqNum)
	require.Contains(t, n.rt.buckets[0].elements, bootstrapNode.ID)
	require.Contains(t, n.rt.buckets[0].elements, extraNode.ID)
}

func TestNode_Republish(t *testing.T) {
	n := newTestNode(0, "3000")
	m := Contact{ID: ParseUint64(1), Address: "3001"}

	k := ParseUint64(5)
	p := []Peer{{ASN: 1, Published: time.Now().UTC().Add(time.Hour)}}
	n.rpc = &mockLookupRpc{
		fn: func(dst Contact, kk Key) ([]Contact, error) {
			require.Equal(t, k, kk)
			return []Contact{m}, nil
		},
		s: func(c Contact, kk Key, pp []Peer) error {
			require.Equal(t, m, c)
			require.Equal(t, k, kk)
			require.Equal(t, p, pp)
			return nil
		},
	}

	n.updateContact(m)
	n.peerStore.Put(k, p)

	n.config.RepublishDur = 100 * time.Millisecond
	go n.republish()
	time.Sleep(1 * time.Second)
	n.stopRepublish <- struct{}{}
}

func TestNode_Refresh(t *testing.T) {
	t.Run("no refresh", func(t *testing.T) {
		n := newTestNode(0, "3000")
		refreshedNode := Contact{ID: ParseUint64(1), Address: "3001"}

		reqNum := atomic.Int64{}
		n.rpc = &mockLookupRpc{
			fn: func(dst Contact, k Key) ([]Contact, error) {
				defer func() {
					reqNum.Add(1)
				}()

				return nil, fmt.Errorf("should not be here")
			},
		}

		// No refresh because our bucket is still in date,
		// so we do not expect any RPCs to be sent
		n.updateContact(refreshedNode)
		n.config.RefreshDur = 100 * time.Millisecond
		go n.refresh()
		time.Sleep(1 * time.Second)
		n.stopRefresh <- struct{}{}
		require.Equal(t, int64(0), reqNum.Load())
	})

	t.Run("yes refresh", func(t *testing.T) {
		n := newTestNode(0, "3000")
		refreshedNode := Contact{ID: ParseUint64(1), Address: "3001"}
		newNode := Contact{ID: ParseUint64(2), Address: "3002"}

		reqNum := atomic.Int64{}
		n.rpc = &mockLookupRpc{
			fn: func(dst Contact, k Key) ([]Contact, error) {
				defer func() {
					reqNum.Add(1)
				}()

				if k == refreshedNode.ID {
					return []Contact{newNode}, nil
				}
				if k == newNode.ID {
					return []Contact{newNode}, nil
				}

				panic("bad rpc")
			},
		}

		// Yes refresh because our bucket is out of date,
		// so we do not expect find node RPCs to be sent
		n.updateContact(refreshedNode)
		n.rt.buckets[0].lastUpdated = time.Now().UTC().Add(-1 * time.Hour)
		n.config.RefreshDur = 100 * time.Millisecond
		go n.refresh()
		time.Sleep(1 * time.Second)
		n.stopRefresh <- struct{}{}

		// We expect 2 queries
		//   1. find node sent to refreshedNode
		//   2. find node sent to newNode
		require.Equal(t, int64(2), reqNum.Load())
	})
}

func TestNode_Ping(t *testing.T) {
	n := newTestNode(0, "3000")
	mux := n.router()

	t.Run("invalid Inu-Src", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/rpc/ping", nil)
		rec := httptest.NewRecorder()
		req.Header.Set("Inu-Swarm", n.config.SwarmKey.MarshalB32())
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid Inu-Swarm", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/rpc/ping", nil)
		rec := httptest.NewRecorder()
		req.Header.Set("Inu-Swarm", ParseUint64(6).MarshalB32())
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("routing table updated", func(t *testing.T) {
		// Contact to be updated
		src := Contact{ID: ParseUint64(1)}

		// Perform the request successfully
		req := httptest.NewRequest("GET", "/rpc/ping", nil)
		addInuSwarmHeaders(req, src, n.config.SwarmKey)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		// Contact should be updated in the routing table
		// in the first (only) bucket
		b := &n.rt.buckets[0]
		require.Equal(t, 1, b.contacts.Len())
		require.Equal(t, src.ID, b.contacts.Front().Value.(Contact).ID)
	})
}

func TestNode_Store(t *testing.T) {
	n := newTestNode(0, "3000")
	mux := n.router()

	t.Run("invalid pair", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/rpc/store", nil)
		addInuSwarmHeaders(req, n.self, n.config.SwarmKey)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("valid pair", func(t *testing.T) {
		// Encode the pair
		p := pair{ParseUint64(1), newTestPeer(1)}
		data, err := json.Marshal(p)
		require.NoError(t, err)

		// Perform the request successfully
		req := httptest.NewRequest("POST", "/rpc/store", bytes.NewReader(data))
		addInuSwarmHeaders(req, n.self, n.config.SwarmKey)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		// Peer set can be retrieved from the store
		v, err := n.peerStore.Get(p.K)
		require.NoError(t, err)
		require.Equal(t, v, p.P)
	})
}

func TestNode_FindNode(t *testing.T) {
	n := newTestNode(255, "3000")
	mux := n.router()

	// Initialise the mock RPC
	mRpc := &mockPingRpc{
		fail: false,
	}

	// Generate 20 contacts in sorted order
	cs := genContacts(20)
	// Insert these into the routing table
	for _, c := range cs {
		n.rt.UpdateContact(c, mRpc)
	}

	// Encode the src contact, must have ID of 0
	k := ParseUint64(0)

	// Perform the request successfully
	req := httptest.NewRequest("GET", "/rpc/find-node/"+k.MarshalB32(), nil)
	addInuSwarmHeaders(req, Contact{ID: k}, n.config.SwarmKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Contact should be updated in the routing table
	var closest []Contact
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&closest))
	require.Equal(t, cs, closest)
}

func TestNode_FindPeers(t *testing.T) {
	n := newTestNode(255, "3000")
	mux := n.router()

	// Initialise the mock RPC
	mRpc := &mockPingRpc{
		fail: false,
	}

	// Generate 20 contacts in sorted order
	cs := genContacts(20)
	// Insert these into the routing table
	for _, c := range cs {
		n.rt.UpdateContact(c, mRpc)
	}

	t.Run("pair not found", func(t *testing.T) {
		// Encode the src contact, must have ID of 0
		k := ParseUint64(0)

		req := httptest.NewRequest("GET", "/rpc/find-peers/"+k.MarshalB32(), nil)
		addInuSwarmHeaders(req, Contact{ID: k}, n.config.SwarmKey)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp findPeersResp
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.False(t, resp.Found)
		require.Equal(t, cs, resp.Contacts)
	})

	t.Run("pair found", func(t *testing.T) {
		// Insert a pair into the store with the
		// same ID as our key (ID must be 0)
		p := pair{ParseUint64(1), newTestPeer(1)}
		n.peerStore.Put(p.K, p.P)

		req := httptest.NewRequest("GET", "/rpc/find-peers/"+p.K.MarshalB32(), nil)
		addInuSwarmHeaders(req, Contact{ID: p.K}, n.config.SwarmKey)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp findPeersResp
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.True(t, resp.Found)
		require.Equal(t, p.P, resp.Peers)
	})
}

func TestNode_GetKey(t *testing.T) {
	t.Run("random asn", func(t *testing.T) {
		n := newTestNode(255, "3000")
		mux := n.router()

		n.updateContact(Contact{ID: ParseUint64(32), Address: "3001"})
		k := ParseUint64(1)
		p := newTestPeer(1)

		n.rpc = &mockLookupRpc{fp: func(dst Contact, kk Key) (findPeersResp, error) {
			if kk == k {
				return findPeersResp{Found: true, Peers: p}, nil
			}
			return findPeersResp{}, fmt.Errorf("not found")
		}}

		t.Run("invalid key", func(t *testing.T) {
			req := httptest.NewRequest("GET", "/key/XYZ", nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})

		t.Run("key not found", func(t *testing.T) {
			k := ParseUint64(0)
			req := httptest.NewRequest("GET", "/key/"+k.MarshalB32(), nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusNotFound, rec.Code)
		})

		t.Run("key found (in store)", func(t *testing.T) {
			n.peerStore.Put(k, p)

			req := httptest.NewRequest("GET", "/key/"+k.MarshalB32(), nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			var peers []Peer
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &peers))
			require.Equal(t, p, peers)
		})

		t.Run("key found (on network)", func(t *testing.T) {
			n.peerStore.Delete(k) // Value does not exist in store so must query network

			req := httptest.NewRequest("GET", "/key/"+k.MarshalB32(), nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			var peers []Peer
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &peers))
			require.Equal(t, p, peers)
		})
	})

	t.Run("asns ordered", func(t *testing.T) {
		n := newTestNode(255, "3000")
		mux := n.router()

		n.updateContact(Contact{ID: ParseUint64(32), Address: "3001"})
		k := ParseUint64(1)

		unsorted := []Peer{{ASN: 8}, {ASN: 5}, {ASN: 7}, {ASN: 5}, {ASN: 6}}
		sorted := []Peer{{ASN: 5}, {ASN: 5}, {ASN: 8}, {ASN: 7}, {ASN: 6}}

		n.rpc = &mockLookupRpc{fp: func(dst Contact, kk Key) (findPeersResp, error) {
			if kk == k {
				return findPeersResp{Found: true, Peers: unsorted}, nil
			}
			return findPeersResp{}, fmt.Errorf("not found")
		}}

		req := httptest.NewRequest("GET", "/key/"+k.MarshalB32(), nil)
		req.RemoteAddr = "192.168.2.1"
		require.NoError(t, n.UpdateASN(netip.MustParsePrefix(req.RemoteAddr+"/24"), 5))
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var peers []Peer
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &peers))
		require.Equal(t, sorted, peers)
	})
}

func TestNode_PutKey(t *testing.T) {
	k := ParseUint64(22)
	newNode := func() *Node {
		conf := DefaultNodeConfig()
		conf.UploadKey = ParseUint64(32)
		return newTestNodeCustom(255, "3000", conf)
	}

	t.Run("could not parse key", func(t *testing.T) {
		n := newNode()
		req := httptest.NewRequest("PUT", "/key/XYZ", nil)
		rec := httptest.NewRecorder()
		n.router().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("could not parse upload key", func(t *testing.T) {
		n := newNode()
		req := httptest.NewRequest("PUT", "/key/"+k.MarshalB32(), nil)
		rec := httptest.NewRecorder()
		req.Header.Set("Inu-Upload", "XYZ")
		n.router().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("no upload key auth", func(t *testing.T) {
		port := uint16(2000)
		data, err := json.Marshal(port)
		require.NoError(t, err)

		n := newNode()
		n.config.UploadKey = Key{}
		n.updateContact(Contact{ID: ParseUint64(1)})
		n.rpc = &mockPingRpc{}

		req := httptest.NewRequest("PUT", "/key/"+k.MarshalB32(), bytes.NewReader(data))
		rec := httptest.NewRecorder()
		n.router().ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		ps, err := n.peerStore.Get(k)
		require.NoError(t, err)
		require.Equal(t, port, ps[0].Port)
		require.Equal(t, netip.MustParseAddrPort(req.RemoteAddr).Addr(), ps[0].IP)
	})

	t.Run("upload key is valid (not on network)", func(t *testing.T) {
		port := uint16(2000)
		data, err := json.Marshal(port)
		require.NoError(t, err)

		n := newNode()
		n.updateContact(Contact{ID: ParseUint64(1)})
		n.rpc = &mockPingRpc{}

		req := httptest.NewRequest("PUT", "/key/"+k.MarshalB32(), bytes.NewReader(data))
		rec := httptest.NewRecorder()
		req.Header.Set("Inu-Upload", n.config.UploadKey.MarshalB32())
		n.router().ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		ps, err := n.peerStore.Get(k)
		require.NoError(t, err)
		require.Equal(t, port, ps[0].Port)
		require.Equal(t, netip.MustParseAddrPort(req.RemoteAddr).Addr(), ps[0].IP)
	})

	t.Run("key is in store so it's valid", func(t *testing.T) {
		n := newNode()

		port := uint16(2000)
		data, err := json.Marshal(port)
		require.NoError(t, err)

		req := httptest.NewRequest("PUT", "/key/"+k.MarshalB32(), bytes.NewReader(data))
		rec := httptest.NewRecorder()
		req.RemoteAddr = "192.168.0.1"
		n.peerStore.Put(k, []Peer{{Port: 60, Published: time.Now().UTC()}})
		n.updateContact(Contact{ID: ParseUint64(1)})
		n.rpc = &mockPingRpc{}

		n.router().ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		ps, err := n.peerStore.Get(k)
		require.NoError(t, err)
		require.Equal(t, uint16(60), ps[0].Port)
		require.Equal(t, uint16(2000), ps[1].Port)
		require.Equal(t, netip.MustParseAddr(req.RemoteAddr), ps[1].IP)
	})

	t.Run("key is on network so it's valid", func(t *testing.T) {
		n := newNode()

		host := "192.168.0.1"
		port := uint16(2000)
		data, err := json.Marshal(port)
		require.NoError(t, err)
		req := httptest.NewRequest("PUT", "/key/"+k.MarshalB32(), bytes.NewReader(data))
		rec := httptest.NewRecorder()
		req.RemoteAddr = host

		c := Contact{ID: ParseUint64(1)}
		n.updateContact(c)
		n.rpc = &mockLookupRpc{
			fn: func(_ Contact, _ Key) ([]Contact, error) {
				return nil, nil
			},
			fp: func(_ Contact, _ Key) (findPeersResp, error) {
				return findPeersResp{Found: true}, nil
			},
			// The value should also be RPCed to be
			// stored across the network
			s: func(cc Contact, kk Key, pp []Peer) error {
				require.Equal(t, c, cc)
				require.Equal(t, k, kk)
				require.Len(t, pp, 1)
				require.Equal(t, port, pp[0].Port)
				return nil
			},
		}

		n.router().ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		ps, err := n.peerStore.Get(k)
		require.NoError(t, err)
		require.Equal(t, port, ps[0].Port)
		require.Equal(t, netip.MustParseAddr(req.RemoteAddr), ps[0].IP)
	})
}

func TestIntegrationNode_BetweenDhtNodes(t *testing.T) {
	// Initial node A, ID = 1
	aConf := DefaultNodeConfig()
	aConf.RepublishDur = 10 * time.Second
	aConf.RefreshDur = 10 * time.Second

	A := newTestNodeCustom(pow2(0), "3000", aConf)
	A.Start()
	defer A.Stop()
	time.Sleep(1 * time.Second) // Wait for node to start

	// Add node B, ID = 2
	B := newTestNode(pow2(1), "3001")
	B.Start()
	defer B.Stop()
	time.Sleep(1 * time.Second) // Wait for node to start
	require.NoError(t, B.Bootstrap(A.self))

	// Add node C, ID = 4
	C := newTestNode(pow2(2), "3002")
	C.Start()
	defer C.Stop()
	time.Sleep(1 * time.Second) // Wait for node to start
	require.NoError(t, C.Bootstrap(A.self))

	// All nodes are propagated into each other's routing table
	A.rt.Lock()
	B.rt.Lock()
	C.rt.Lock()
	require.Contains(t, A.rt.buckets[0].elements, B.self.ID)
	require.Contains(t, A.rt.buckets[0].elements, C.self.ID)
	require.Contains(t, B.rt.buckets[0].elements, A.self.ID)
	require.Contains(t, B.rt.buckets[0].elements, C.self.ID)
	require.Contains(t, C.rt.buckets[0].elements, A.self.ID)
	require.Contains(t, C.rt.buckets[0].elements, B.self.ID)
	A.rt.Unlock()
	B.rt.Unlock()
	C.rt.Unlock()

	// Define the values we will store on the network
	pubTime := time.Now().UTC().Add(-5 * time.Second)

	p1 := pair{K: ParseUint64(21), P: []Peer{{ASN: 1, Published: pubTime}}}
	p2 := pair{K: ParseUint64(22), P: []Peer{{ASN: 2, Published: pubTime}}}
	p3 := pair{K: ParseUint64(23), P: []Peer{{ASN: 3, Published: pubTime}}}

	// Store P1 on A
	// Store P2 on B
	// Store P3 on C
	require.NoError(t, A.Store(p1.K, p1.P))
	require.NoError(t, B.Store(p2.K, p2.P))
	require.NoError(t, C.Store(p3.K, p3.P))

	// Each pair is accessible from all nodes in the swarm
	for _, p := range []pair{p1, p2, p3} {
		for _, n := range []*Node{A, B, C} {
			v, err := n.peerStore.Get(p.K)
			require.NoError(t, err)
			require.Equal(t, p.P, v)
		}
	}

	// Add node D, ID = 8
	D := newTestNode(pow2(3), "3003")
	D.Start()
	defer D.Stop()
	time.Sleep(1 * time.Second) // Wait for node to start
	require.NoError(t, D.Bootstrap(B.self))

	// Add node E, ID = 16
	E := newTestNode(pow2(4), "3004")
	E.Start()
	defer E.Stop()
	time.Sleep(1 * time.Second) // Wait for node to start
	require.NoError(t, E.Bootstrap(B.self))

	// All nodes should be aware of each other, they are
	// all guaranteed to be in the same bucket because the
	// default k is large enough
	A.rt.Lock()
	B.rt.Lock()
	C.rt.Lock()
	D.rt.Lock()
	E.rt.Lock()
	for _, n := range []*Node{A, B, C, D, E} {
		for _, m := range []*Node{A, B, C, D, E} {
			if n.self.ID == m.self.ID {
				continue
			}

			require.Contains(t, n.rt.buckets[0].elements, m.self.ID)
		}
	}
	A.rt.Unlock()
	B.rt.Unlock()
	C.rt.Unlock()
	D.rt.Unlock()
	E.rt.Unlock()

	// The keys should be republished to the new nodes D and E
	// (A will republish them)
	require.Eventually(t, func() bool {
		for _, p := range []pair{p1, p2, p3} {
			for _, n := range []*Node{D, E} {
				v, err := n.peerStore.Get(p.K)
				if err != nil || !assert.ObjectsAreEqual(p.P, v) {
					return false
				}
			}
		}
		return true
	}, 20*time.Second, 1*time.Second)

	// We add F to A and G to B, but the bucket for F is manually
	// updated so that it requires a refresh. We then expect A to
	// refresh F thereby adding G to its own routing table.
	F := newTestNode(pow2(32), "3032")
	G := newTestNode(pow2(33), "3033")
	for _, n := range []*Node{F, G} {
		n.Start()
		defer n.Stop()
	}
	time.Sleep(1 * time.Second) // Wait for node to start

	A.rt.UpdateContact(F.self, A.rpc)
	A.rt.Lock()
	i := A.rt.bucket(F.self.ID)
	A.rt.buckets[i].lastUpdated = time.Now().UTC().Add(-25 * time.Hour)
	A.rt.Unlock()
	B.rt.UpdateContact(G.self, B.rpc)

	// A refreshes F and adds G to its routing table
	require.Eventually(t, func() bool {
		A.rt.Lock()
		defer A.rt.Unlock()

		_, found := A.rt.buckets[0].elements[G.self.ID]
		return found
	}, 20*time.Second, 1*time.Second)

	// Key expiry for all nodes is set to 1 second
	expiry := 1 * time.Millisecond
	A.peerStore.expiry = expiry
	B.peerStore.expiry = expiry
	C.peerStore.expiry = expiry
	D.peerStore.expiry = expiry
	E.peerStore.expiry = expiry
	time.Sleep(expiry)

	// We expect the keys to expire from all nodes
	// once we access their stores
	for _, p := range []pair{p1, p2, p3} {
		for _, n := range []*Node{A, B, C, D, E} {
			v, err := n.peerStore.Get(p.K)
			require.ErrorContains(t, err, "all peers dead")
			require.Nil(t, v)
		}
	}

	// Each store should be completely empty now
	for _, n := range []*Node{A, B, C, D, E} {
		n.peerStore.Lock()
		require.Len(t, n.peerStore.data, 0)
		n.peerStore.Unlock()
	}
}

func TestIntegrationNode_AuthedUpload(t *testing.T) {
	// Valid upload key is supplied, so we can
	// upload a key-peer pair to an empty network
	//
	// We do not want any replication to happen in
	// this test, because we want keys to only
	// move around the network on request

	uploadKey := ParseUint64(56)
	client := cert.Client(10 * time.Second)
	localhost := netip.MustParseAddr("127.0.0.1")

	// Initial node A, ID = 1
	A := newTestNode(pow2(0), "3000")
	A.config.UploadKey = uploadKey
	require.NoError(t, A.UpdateASN(netip.PrefixFrom(localhost, 1), 1))
	A.Start()
	defer A.Stop()
	time.Sleep(1 * time.Second) // Wait for node to start

	// Add node B, ID = 2
	B := newTestNode(pow2(1), "3001")
	B.Start()
	defer B.Stop()
	time.Sleep(1 * time.Second) // Wait for node to start
	require.NoError(t, B.Bootstrap(A.self))

	// Nodes are aware of each other
	t.Run("A B routing tables updated", func(t *testing.T) {
		A.rt.Lock()
		B.rt.Lock()
		require.Contains(t, A.rt.buckets[0].elements, B.self.ID)
		require.Contains(t, B.rt.buckets[0].elements, A.self.ID)
		A.rt.Unlock()
		B.rt.Unlock()
	})

	// Authenticate an upload of the peer set to A
	k := ParseUint64(5)
	var ps []Peer

	t.Run("authed upload to A", func(t *testing.T) {
		// Encode the port
		port := uint16(60)
		data, err := json.Marshal(port)
		require.NoError(t, err)

		// PUT this to A with an upload key
		url := "https://" + A.self.Address + "/key/" + k.MarshalB32()
		req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
		require.NoError(t, err)
		req.Header.Add("Inu-Upload", A.config.UploadKey.MarshalB32())

		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)

		// Verify peer exists on A and B
		peersA, err := A.peerStore.Get(k)
		require.NoError(t, err)
		require.Len(t, peersA, 1)
		require.Equal(t, localhost, peersA[0].IP)
		require.Equal(t, port, peersA[0].Port)
		require.WithinDuration(t, time.Now(), peersA[0].Published, 3*time.Second)
		require.Equal(t, 1, peersA[0].ASN) // The ASN for localhost on A is 1

		peersB, err := B.peerStore.Get(k)
		require.NoError(t, err)
		require.Equal(t, peersA, peersB)

		ps = peersA // Store peers so other subtests can access them
	})

	// Add node C, ID = 4
	C := newTestNode(pow2(2), "3002")
	C.Start()
	defer C.Stop()
	time.Sleep(1 * time.Second) // Wait for node to start
	require.NoError(t, C.Bootstrap(B.self))

	// Nodes are aware of each other
	t.Run("A B C routing tables updated", func(t *testing.T) {
		A.rt.Lock()
		B.rt.Lock()
		C.rt.Lock()
		for _, n := range []*Node{A, B, C} {
			for _, m := range []*Node{A, B, C} {
				if n.self.ID == m.self.ID {
					continue
				}
				require.Contains(t, n.rt.buckets[0].elements, m.self.ID)
			}
		}
		A.rt.Unlock()
		B.rt.Unlock()
		C.rt.Unlock()
	})

	t.Run("get peers from C", func(t *testing.T) {
		// C should not have any peers in its store for
		// the key. We want to see it query the network
		// for the key
		_, err := C.peerStore.Get(k)
		require.ErrorContains(t, err, "key not found")

		// GET to C for the key
		url := "https://" + C.self.Address + "/key/" + k.MarshalB32()
		req, err := http.NewRequest("GET", url, nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		var peers []Peer
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&peers))
		require.Equal(t, ps, peers)
	})

	// Add node D, ID = 8
	D := newTestNode(pow2(3), "3003")
	require.NoError(t, D.UpdateASN(netip.PrefixFrom(localhost, 1), 2))
	D.Start()
	defer D.Stop()
	time.Sleep(1 * time.Second) // Wait for node to start
	require.NoError(t, D.Bootstrap(C.self))

	// Nodes are aware of each other
	t.Run("A B C D routing tables updated", func(t *testing.T) {
		A.rt.Lock()
		B.rt.Lock()
		C.rt.Lock()
		D.rt.Lock()
		for _, n := range []*Node{A, B, C, D} {
			for _, m := range []*Node{A, B, C, D} {
				if n.self.ID == m.self.ID {
					continue
				}
				require.Contains(t, n.rt.buckets[0].elements, m.self.ID)
			}
		}
		A.rt.Unlock()
		B.rt.Unlock()
		C.rt.Unlock()
		D.rt.Unlock()
	})

	t.Run("non-authed upload to D", func(t *testing.T) {
		// Encode the port
		port := uint16(61) // Should not be equal to the earlier port number
		data, err := json.Marshal(port)
		require.NoError(t, err)

		// PUT this to D, it should be fine not using
		// an upload key because the key exists on the
		// network (albeit not on D)
		url := "https://" + D.self.Address + "/key/" + k.MarshalB32()
		req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)

		// Verify peer exists on A B C D
		peersA, err := A.peerStore.Get(k)
		require.NoError(t, err)
		require.Len(t, peersA, 2)

		slices.SortFunc(peersA, func(a, b Peer) int {
			return int(a.Port) - int(b.Port)
		})

		require.Equal(t, localhost, peersA[0].IP)
		require.Equal(t, uint16(60), peersA[0].Port)
		require.WithinDuration(t, time.Now(), peersA[0].Published, 3*time.Second)
		require.Equal(t, 1, peersA[0].ASN)

		require.Equal(t, localhost, peersA[1].IP)
		require.Equal(t, uint16(61), peersA[1].Port)
		require.WithinDuration(t, time.Now(), peersA[1].Published, 3*time.Second)
		require.Equal(t, 2, peersA[1].ASN) // The ASN for localhost on D is 2, and we uploaded to D

		// B and C should have the full peer list
		for _, n := range []*Node{B, C} {
			peers, err := n.peerStore.Get(k)
			require.NoError(t, err)
			require.Equal(t, len(peersA), len(peers))
			for _, p := range peersA {
				require.Contains(t, peers, p)
			}
		}

		// D should only have the newly uploaded peer
		// because no replication has happened, and it
		// does not automatically store get peer queries
		peersD, err := D.peerStore.Get(k)
		require.NoError(t, err)
		require.Len(t, peersD, 1)
		require.Equal(t, localhost, peersD[0].IP)
		require.Equal(t, uint16(61), peersD[0].Port)
		require.WithinDuration(t, time.Now(), peersD[0].Published, 3*time.Second)
		require.Equal(t, 2, peersD[0].ASN) // The ASN for localhost on D is 2, and we uploaded to D
	})
}

func TestIntegrationNode_Refreshing(t *testing.T) {
	refreshConf := DefaultNodeConfig()
	refreshConf.RefreshDur = 5 * time.Second

	src := newTestNodeCustom(pow2(0), "3000", refreshConf)
	A := newTestNode(pow2(1), "3001")

	// Set all buckets as needing to be refreshed
	before := time.Now().Add(-25 * time.Hour)
	for i := range src.rt.buckets {
		src.rt.buckets[i].lastUpdated = before
	}

	// Set up the servers
	src.rt.UpdateContact(A.self, src.rpc)
	for _, n := range []*Node{src, A} {
		n.Start()
		defer n.Stop()
	}

	// Src republishes the bucket after 5 seconds
	time.Sleep(6 * time.Second)
	src.rt.Lock()
	i := src.rt.bucket(A.self.ID)
	require.NotEqual(t, before, src.rt.buckets[i].lastUpdated)
	src.rt.Unlock()
}

func TestIntegrationNode_Republishing(t *testing.T) {
	republishConf := DefaultNodeConfig()
	republishConf.RepublishDur = 5 * time.Second

	src := newTestNodeCustom(pow2(0), "3000", republishConf)
	A := newTestNode(pow2(1), "3001")
	B := newTestNode(pow2(2), "3002")
	C := newTestNode(pow2(3), "3003")

	// Store the block in src
	k := ParseUint64(1)
	ps := newTestPeer(1)
	src.peerStore.Put(k, ps)

	// Src knows A B C
	src.rt.UpdateContact(A.self, src.rpc)
	src.rt.UpdateContact(B.self, src.rpc)
	src.rt.UpdateContact(C.self, src.rpc)

	// Set up the servers
	for _, n := range []*Node{src, A, B, C} {
		n.Start()
		defer n.Stop()
	}

	// Src republishes the block to A B C after about 5 seconds
	time.Sleep(7 * time.Second)

	v, err := A.peerStore.Get(k)
	require.NoError(t, err)
	require.Equal(t, ps, v)
	v, err = B.peerStore.Get(k)
	require.NoError(t, err)
	require.Equal(t, ps, v)
	v, err = C.peerStore.Get(k)
	require.NoError(t, err)
	require.Equal(t, ps, v)
}

// Lookup

func TestLookup_Store(t *testing.T) {
	src := newTestNode(pow2(9), "3000")
	A := newTestNode(pow2(8), "3001")
	B := newTestNode(pow2(7), "3002")
	C := newTestNode(pow2(6), "3003")
	D := newTestNode(pow2(5), "3004")

	found := make(map[Key]int)
	stored := make(map[Key]int)
	m := sync.Mutex{}

	k := ParseUint64(1)
	p := newTestPeer(1)
	src.rpc = &mockLookupRpc{
		fn: func(dst Contact, kk Key) ([]Contact, error) {
			m.Lock()
			found[dst.ID] += 1
			m.Unlock()
			require.Equal(t, k, kk)
			return []Contact{A.self, B.self, C.self, D.self}, nil
		},
		s: func(c Contact, kk Key, pp []Peer) error {
			m.Lock()
			stored[c.ID] += 1
			m.Unlock()
			require.Equal(t, k, kk)
			require.Equal(t, p, pp)
			return nil
		},
	}
	src.updateContact(A.self)

	// Should make 4 find requests in addition
	// to 4 store RPCs
	require.NoError(t, src.Store(k, p))
	require.Len(t, found, 4)
	require.Equal(t, 1, found[A.self.ID])
	require.Equal(t, 1, found[B.self.ID])
	require.Equal(t, 1, found[C.self.ID])
	require.Equal(t, 1, found[D.self.ID])
	require.Len(t, stored, 4)
	require.Equal(t, 1, stored[A.self.ID])
	require.Equal(t, 1, stored[B.self.ID])
	require.Equal(t, 1, stored[C.self.ID])
	require.Equal(t, 1, stored[D.self.ID])

	// The stored value should also be present in
	// our own store
	pp, err := src.peerStore.Get(k)
	require.NoError(t, err)
	require.Equal(t, p, pp)
}

func TestLookup_FindNode(t *testing.T) {
	src := newTestNode(0, "3000")
	target := newTestNode(pow2(2), "3001")
	A := newTestNode(pow2(4), "3002")

	src.rpc = &mockLookupRpc{
		fn: func(dst Contact, k Key) ([]Contact, error) {
			if dst == A.self && k == target.self.ID {
				return []Contact{target.self}, nil
			}
			if dst == target.self && k == target.self.ID {
				return []Contact{target.self}, nil
			}

			panic("bad rpc")
		},
	}
	src.updateContact(A.self)

	cs, err := src.FindNode(target.self.ID)
	require.NoError(t, err)
	require.Len(t, cs, 2)
	require.Equal(t, target.self, cs[0])
	require.Equal(t, A.self, cs[1])
}

func TestLookup_FindPeers(t *testing.T) {
	t.Run("rejects nodes", func(t *testing.T) {
		src := newTestNode(0, "3000")
		target := newTestNode(pow2(2), "3001")
		A := newTestNode(pow2(4), "3002")

		src.rpc = &mockLookupRpc{
			fp: func(dst Contact, k Key) (findPeersResp, error) {
				if dst == A.self && k == target.self.ID {
					return findPeersResp{Contacts: []Contact{target.self}}, nil
				}
				if dst == target.self && k == target.self.ID {
					return findPeersResp{Contacts: []Contact{target.self}}, nil
				}

				panic("bad rpc")
			},
		}
		src.updateContact(A.self)

		ps, err := src.FindPeers(target.self.ID)
		require.ErrorContains(t, err, "could not find peers who provide key")
		require.Nil(t, ps)
	})

	t.Run("successful", func(t *testing.T) {
		src := newTestNode(0, "3000")
		A := newTestNode(pow2(4), "3002")

		src.updateContact(A.self)
		k := ParseUint64(1)
		p := []Peer{{ASN: 1}}
		src.rpc = &mockLookupRpc{
			fp: func(dst Contact, kk Key) (findPeersResp, error) {
				if dst == A.self && kk == k {
					return findPeersResp{
						Found: true,
						Peers: p,
					}, nil
				}

				panic("bad rpc")
			},
		}

		pp, err := src.FindPeers(k)
		require.NoError(t, err)
		require.Equal(t, p, pp)
	})
}

func TestLookup_Errors(t *testing.T) {
	conf := DefaultNodeConfig()
	conf.K = 1
	conf.Alpha = 1

	t.Run("empty routing table", func(t *testing.T) {
		src := newTestNodeCustom(pow2(63), "3000", conf)
		resp, err := src.lookup(ParseUint64(pow2(1)), findNodeLookup)
		require.ErrorContains(t, err, "no close nodes")
		require.Equal(t, resp, findPeersResp{})
	})

	t.Run("all contacts unreachable", func(t *testing.T) {
		src := newTestNodeCustom(pow2(63), "3000", conf)
		target := newTestNodeCustom(pow2(2), "3001", conf)

		// We add one contact which fails to reply to the RPC
		// since it's unreachable
		src.rt.UpdateContact(target.self, src.rpc)
		resp, err := src.lookup(target.self.ID, findNodeLookup)
		require.ErrorContains(t, err, "all contacts unreachable")
		require.Equal(t, resp, findPeersResp{})
	})
}

func TestLookup_EdgeCases(t *testing.T) {
	t.Run("unreachable nodes discarded", func(t *testing.T) {
		// Only one intermediate node is reachable

		src := newTestNode(0, "3000")
		A := newTestNode(pow2(2), "3001")
		B := newTestNode(pow2(4), "3002")
		target := newTestNode(pow2(6), "3003")

		// We must have both intermediate contacts
		// in the starting pool and have one of them
		// be unreachable
		require.GreaterOrEqual(t, src.config.Alpha, 2)
		src.rt.UpdateContact(A.self, src.rpc)
		src.rt.UpdateContact(B.self, src.rpc)

		src.rpc = &mockLookupRpc{
			fn: func(dst Contact, k Key) ([]Contact, error) {
				if dst == A.self {
					return nil, fmt.Errorf("cannot reach A")
				}
				if dst == B.self && k == target.self.ID {
					return []Contact{target.self}, nil
				}
				if dst == target.self && k == target.self.ID {
					return []Contact{target.self}, nil
				}

				panic("bad rpc")
			},
		}

		resp, err := src.lookup(target.self.ID, findNodeLookup)
		require.NoError(t, err)
		cs := resp.Contacts
		require.False(t, resp.Found)
		require.Len(t, cs, 2)
		require.Equal(t, target.self, cs[0])
		require.Equal(t, B.self, cs[1])
	})

	t.Run("alpha restricts initial node count", func(t *testing.T) {
		// Each subtest has 3 nodes but depending
		// on the alpha we have a different number
		// of final closest nodes because the initial
		// node set is restricted to alpha nodes

		src := newTestNode(pow2(0), "3000")
		A := newTestNode(pow2(2), "3001")
		B := newTestNode(pow2(3), "3002")
		C := newTestNode(pow2(4), "3003")
		target := newTestNode(pow2(6), "3004")

		src.rt.UpdateContact(A.self, src.rpc)
		src.rt.UpdateContact(B.self, src.rpc)
		src.rt.UpdateContact(C.self, src.rpc)

		src.rpc = &mockLookupRpc{
			fn: func(dst Contact, k Key) ([]Contact, error) {
				if k == target.self.ID {
					return []Contact{target.self}, nil
				}

				panic("bad rpc")
			},
		}

		t.Run("a = 1", func(t *testing.T) {
			src.config.Alpha = 1

			// We expect the target and the closest node (A)
			resp, err := src.lookup(target.self.ID, findNodeLookup)
			require.NoError(t, err)
			cs := resp.Contacts
			require.False(t, resp.Found)
			require.Len(t, cs, 2)
			require.Equal(t, target.self, cs[0])
			require.Equal(t, A.self, cs[1])
		})

		t.Run("a = 2", func(t *testing.T) {
			src.config.Alpha = 2

			// We expect the target and the closest two nodes (A, B)
			resp, err := src.lookup(target.self.ID, findNodeLookup)
			require.NoError(t, err)
			cs := resp.Contacts
			require.False(t, resp.Found)
			require.Len(t, cs, 3)
			require.Equal(t, target.self, cs[0])
			require.Equal(t, A.self, cs[1])
			require.Equal(t, B.self, cs[2])
		})

		t.Run("a = 3", func(t *testing.T) {
			src.config.Alpha = 3

			// We expect the target and the closest three nodes (A, B, C)
			resp, err := src.lookup(target.self.ID, findNodeLookup)
			require.NoError(t, err)
			cs := resp.Contacts
			require.False(t, resp.Found)
			require.Len(t, cs, 4)
			require.Equal(t, target.self, cs[0])
			require.Equal(t, A.self, cs[1])
			require.Equal(t, B.self, cs[2])
			require.Equal(t, C.self, cs[3])
		})
	})

	t.Run("k restricts the final node count", func(t *testing.T) {
		// Each subtest has 3 nodes but depending
		// on the k we have a different number
		// of final closest nodes because we only
		// have a max of k nodes each round

		src := newTestNode(pow2(0), "3000")
		A := newTestNode(pow2(2), "3001")
		B := newTestNode(pow2(3), "3002")
		C := newTestNode(pow2(4), "3003")
		target := newTestNode(pow2(6), "3004")

		src.rt.UpdateContact(A.self, src.rpc)
		src.rt.UpdateContact(B.self, src.rpc)
		src.rt.UpdateContact(C.self, src.rpc)

		src.rpc = &mockLookupRpc{
			fn: func(dst Contact, k Key) ([]Contact, error) {
				if k == target.self.ID {
					return []Contact{target.self}, nil
				}

				panic("bad rpc")
			},
		}

		t.Run("k = 1", func(t *testing.T) {
			src.config.K = 1

			// We expect the target
			resp, err := src.lookup(target.self.ID, findNodeLookup)
			require.NoError(t, err)
			cs := resp.Contacts
			require.False(t, resp.Found)
			require.Len(t, cs, 1)
			require.Equal(t, target.self, cs[0])
		})

		t.Run("k = 2", func(t *testing.T) {
			src.config.K = 2

			// We expect the target and the closes node (A)
			resp, err := src.lookup(target.self.ID, findNodeLookup)
			require.NoError(t, err)
			cs := resp.Contacts
			require.False(t, resp.Found)
			require.Len(t, cs, 2)
			require.Equal(t, target.self, cs[0])
			require.Equal(t, A.self, cs[1])
		})

		t.Run("k = 3", func(t *testing.T) {
			src.config.K = 3

			// We expect the target and the closest two nodes (A, B)
			resp, err := src.lookup(target.self.ID, findNodeLookup)
			require.NoError(t, err)
			cs := resp.Contacts
			require.False(t, resp.Found)
			require.Len(t, cs, 3)
			require.Equal(t, target.self, cs[0])
			require.Equal(t, A.self, cs[1])
			require.Equal(t, B.self, cs[2])
		})
	})

	t.Run("do not re-contact nodes", func(t *testing.T) {
		// Src knows A
		// A   knows B
		// B   knows A and C
		// C   knows Target
		// We only contact A once

		alphaConf := DefaultNodeConfig()
		alphaConf.Alpha = 1

		src := newTestNodeCustom(pow2(0), "3000", alphaConf)
		A := newTestNode(pow2(2), "3001")
		B := newTestNode(pow2(3), "3002")
		C := newTestNode(pow2(4), "3003")
		target := newTestNode(pow2(6), "3004")

		src.rt.UpdateContact(A.self, src.rpc)

		count := make(map[Key]int)
		src.rpc = &mockLookupRpc{
			fn: func(dst Contact, k Key) ([]Contact, error) {
				count[dst.ID] += 1

				if dst == A.self && k == target.self.ID {
					return []Contact{B.self}, nil
				}
				if dst == B.self && k == target.self.ID {
					return []Contact{A.self, C.self}, nil
				}
				if dst == C.self && k == target.self.ID {
					return []Contact{target.self}, nil
				}
				if dst == target.self && k == target.self.ID {
					return []Contact{target.self}, nil
				}

				panic("bad rpc")
			},
		}

		resp, err := src.lookup(target.self.ID, findNodeLookup)
		require.NoError(t, err)
		cs := resp.Contacts
		require.False(t, resp.Found)
		require.Len(t, cs, 4)
		require.Equal(t, target.self, cs[0])
		require.Equal(t, A.self, cs[1])
		require.Equal(t, B.self, cs[2])
		require.Equal(t, C.self, cs[3])
		require.Equal(t, 1, count[A.self.ID])
	})

	t.Run("remove on timeout and reinsert on successful reply", func(t *testing.T) {
		alphaConf := DefaultNodeConfig()
		alphaConf.Alpha = 2

		src := newTestNodeCustom(pow2(0), "3000", alphaConf)
		A := newTestNode(pow2(2), "3001")
		B := newTestNode(pow2(4), "3002")
		C := newTestNode(pow2(6), "3003")
		target := newTestNode(pow2(8), "3004")

		src.rt.UpdateContact(A.self, src.rpc)
		src.rt.UpdateContact(B.self, src.rpc)

		contactedA := false
		src.rpc = &mockLookupRpc{
			fn: func(dst Contact, k Key) ([]Contact, error) {
				if dst == A.self && k == target.self.ID {
					if !contactedA {
						time.Sleep(6 * time.Second)
						contactedA = true
					}
					return []Contact{C.self}, nil
				}
				if dst == B.self && k == target.self.ID {
					return []Contact{C.self}, nil
				}
				if dst == C.self && k == target.self.ID {
					time.Sleep(2 * time.Second)
					return []Contact{target.self}, nil
				}
				if dst == target.self && k == target.self.ID {
					return []Contact{target.self}, nil
				}

				panic("bad rpc")
			},
		}

		// A gets removed and then added back again
		// which leads us to have 4 closest nodes
		resp, err := src.lookup(target.self.ID, findNodeLookup)
		require.NoError(t, err)
		cs := resp.Contacts
		require.False(t, resp.Found)
		require.Len(t, cs, 4)
		require.Equal(t, target.self, cs[0])
		require.Equal(t, A.self, cs[1])
		require.Equal(t, B.self, cs[2])
		require.Equal(t, C.self, cs[3])
	})
}

func TestLookup_Success(t *testing.T) {
	t.Run("direct lookup", func(t *testing.T) {
		src := newTestNode(pow2(0), "3000")
		target := newTestNode(pow2(2), "3001")

		src.rpc = &mockLookupRpc{
			fn: func(dst Contact, k Key) ([]Contact, error) {
				if dst == target.self && k == target.self.ID {
					return []Contact{target.self}, nil
				}

				panic("bad rpc")
			},
		}
		src.updateContact(target.self)

		resp, err := src.lookup(target.self.ID, findNodeLookup)
		require.NoError(t, err)
		cs := resp.Contacts
		require.False(t, resp.Found)
		require.Len(t, cs, 1)
		require.Equal(t, target.self, cs[0])
	})

	t.Run("indirect lookup (one degree)", func(t *testing.T) {
		src := newTestNode(pow2(0), "3000")
		target := newTestNode(pow2(2), "3001")
		intermediate := newTestNode(pow2(4), "3002")

		src.rpc = &mockLookupRpc{
			fn: func(dst Contact, k Key) ([]Contact, error) {
				if dst == intermediate.self && k == target.self.ID {
					return []Contact{target.self}, nil
				}
				if dst == target.self && k == target.self.ID {
					return []Contact{target.self}, nil
				}

				panic("bad rpc")
			},
		}
		src.updateContact(intermediate.self)

		resp, err := src.lookup(target.self.ID, findNodeLookup)
		require.NoError(t, err)
		cs := resp.Contacts
		require.False(t, resp.Found)
		require.Len(t, cs, 2)
		require.Equal(t, cs[0], target.self)
		require.Equal(t, cs[1], intermediate.self)
	})

	t.Run("indirect lookup (three degrees)", func(t *testing.T) {
		src := newTestNode(pow2(0), "3000")
		target := newTestNode(pow2(2), "3001")

		// Src knows A
		// A   knows B
		// B   knows C
		// C   knows Target
		A := newTestNode(pow2(4), "3002")
		B := newTestNode(pow2(6), "3003")
		C := newTestNode(pow2(8), "3004")

		src.rpc = &mockLookupRpc{
			fn: func(dst Contact, k Key) ([]Contact, error) {
				if dst == A.self && k == target.self.ID {
					return []Contact{B.self}, nil
				}
				if dst == B.self && k == target.self.ID {
					return []Contact{C.self}, nil
				}
				if dst == C.self && k == target.self.ID {
					return []Contact{target.self}, nil
				}
				if dst == target.self && k == target.self.ID {
					return []Contact{target.self}, nil
				}

				panic("bad rpc")
			},
		}
		src.updateContact(A.self)

		resp, err := src.lookup(target.self.ID, findNodeLookup)
		require.NoError(t, err)
		cs := resp.Contacts
		require.False(t, resp.Found)
		require.Len(t, cs, 4)
		require.Equal(t, target.self, cs[0])
		require.Equal(t, A.self, cs[1])
		require.Equal(t, B.self, cs[2])
		require.Equal(t, C.self, cs[3])
	})
}

func TestIntegrationLookup_Store(t *testing.T) {
	src := newTestNode(pow2(0), "3000")
	A := newTestNode(pow2(1), "3001")
	B := newTestNode(pow2(2), "3002")
	C := newTestNode(pow2(3), "3003")

	// Src knows A B C
	src.updateContact(A.self)
	src.updateContact(B.self)
	src.updateContact(C.self)

	// Set up the servers
	for _, n := range []*Node{src, A, B, C} {
		n.Start()
		defer n.Stop()
	}
	time.Sleep(1 * time.Second)

	// Store the peers and check it exists
	// on the possible neighbours (these
	// are the three at pre-exist in the
	// routing table)
	k := ParseUint64(1)
	ps := newTestPeer(1)
	require.NoError(t, src.Store(k, ps))

	v, err := A.peerStore.Get(k)
	require.NoError(t, err)
	require.Equal(t, ps, v)
	v, err = B.peerStore.Get(k)
	require.NoError(t, err)
	require.Equal(t, ps, v)
	v, err = C.peerStore.Get(k)
	require.NoError(t, err)
	require.Equal(t, ps, v)

	// Can use find peers to get the peers back
	v, err = src.FindPeers(k)
	require.NoError(t, err)
	require.Equal(t, v, ps)
}

func TestIntegrationLookup_FindNode(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		src := newTestNode(pow2(0), "3000")
		target := newTestNode(pow2(2), "3001")

		target.Start()
		defer target.Stop()
		time.Sleep(1 * time.Second)

		src.rt.UpdateContact(target.self, src.rpc)
		cs, err := src.FindNode(target.self.ID)
		require.NoError(t, err)
		require.Len(t, cs, 1)
		require.Equal(t, target.self, cs[0])
	})

	t.Run("complex", func(t *testing.T) {
		conf := DefaultNodeConfig()
		conf.K = 4

		// Nodes = [Src, A, B, C, D, E, F, G, Target]
		src := newTestNodeCustom(pow2(8), "3000", conf)
		A := newTestNodeCustom(pow2(7), "3001", conf)
		B := newTestNodeCustom(pow2(6), "3002", conf)
		C := newTestNodeCustom(pow2(5), "3003", conf)
		D := newTestNodeCustom(pow2(4), "3004", conf)
		E := newTestNodeCustom(pow2(3), "3005", conf)
		F := newTestNodeCustom(pow2(2), "3006", conf)
		G := newTestNodeCustom(pow2(1), "3007", conf)
		target := newTestNodeCustom(pow2(0), "3008", conf)

		// Src knows A, B
		src.rt.UpdateContact(A.self, src.rpc)
		src.rt.UpdateContact(B.self, src.rpc)

		// A knows B, C, E
		A.rt.UpdateContact(B.self, A.rpc)
		A.rt.UpdateContact(C.self, A.rpc)
		A.rt.UpdateContact(E.self, A.rpc)

		// B knows A, C, D
		B.rt.UpdateContact(A.self, B.rpc)
		B.rt.UpdateContact(C.self, B.rpc)
		B.rt.UpdateContact(D.self, B.rpc)

		// C knows F G
		C.rt.UpdateContact(F.self, C.rpc)
		C.rt.UpdateContact(G.self, C.rpc)

		// D knows F G
		D.rt.UpdateContact(F.self, D.rpc)
		D.rt.UpdateContact(G.self, D.rpc)

		// E knows F Target
		E.rt.UpdateContact(F.self, E.rpc)
		E.rt.UpdateContact(target.self, E.rpc)

		// Set up the servers
		for _, n := range []*Node{src, A, B, C, D, E, F, G, target} {
			n.Start()
			defer n.Stop()
		}

		// Round 1
		// Closest = [E, D, C, B]
		// Round 2
		// Closest = [Target, G, F, E]
		// Round 3
		// Target is the closest node
		cs, err := src.FindNode(target.self.ID)
		require.NoError(t, err)
		require.Len(t, cs, 4)
		require.Equal(t, target.self, cs[0])
		require.Equal(t, G.self, cs[1])
		require.Equal(t, F.self, cs[2])
		require.Equal(t, E.self, cs[3])
	})
}

func TestIntegrationLookup_FindPeers(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		// Init the servers and target peer set
		src := newTestNode(pow2(0), "3000")
		A := newTestNode(pow2(1), "3001")

		k := ParseUint64(1)
		p := newTestPeer(1)

		// Update the routing table and store
		A.Start()
		defer A.Stop()
		time.Sleep(1 * time.Second)

		src.rt.UpdateContact(A.self, src.rpc)
		A.peerStore.Put(k, p)

		// Perform the lookup
		v, err := src.FindPeers(k)
		require.NoError(t, err)
		require.Equal(t, p, v)
	})

	t.Run("complex", func(t *testing.T) {
		src := newTestNode(pow2(8), "3000")
		E := newTestNode(pow2(3), "3005")
		F := newTestNode(pow2(2), "3006")
		target := newTestNode(pow2(0), "3008")

		k := ParseUint64(1)
		p := newTestPeer(1)

		// Src knows E
		src.rt.UpdateContact(E.self, src.rpc)

		// E knows F Target
		E.rt.UpdateContact(F.self, E.rpc)
		E.rt.UpdateContact(target.self, E.rpc)

		// Target owns the target peer set
		target.peerStore.Put(k, p)

		// Set up the servers
		for _, n := range []*Node{src, E, F, target} {
			n.Start()
			defer n.Stop()
		}

		// Round 1
		// Closest = [E]
		// Round 2
		// Closest = [F, Target]
		// Round 3
		// Block found
		v, err := src.FindPeers(k)
		require.NoError(t, err)
		require.Equal(t, p, v)

		// The kv pair is cached at the nearest node which
		// did not have the value (F)
		v, err = F.peerStore.Get(k)
		require.NoError(t, err)
		require.Equal(t, p, v)
	})
}

// Shortlist

func TestShortlist_New(t *testing.T) {
	target := ParseUint64(0)
	s := newShortlist(target, 20)
	require.Equal(t, 20, s.k)
	require.Equal(t, target, s.target)
	require.NotNil(t, s.contacted)
	require.NotNil(t, s.list)
}

func TestShortlist_Insert(t *testing.T) {
	target := ParseUint64(0)

	t.Run("restricted to k", func(t *testing.T) {
		c1 := Contact{ID: ParseUint64(1)}
		c2 := Contact{ID: ParseUint64(2)}
		c3 := Contact{ID: ParseUint64(3)}
		c4 := Contact{ID: ParseUint64(4)}
		c5 := Contact{ID: ParseUint64(5)}
		c6 := Contact{ID: ParseUint64(6)}

		s := newShortlist(target, 5)
		s.Insert(c5, c2, c3, c1, c6, c4)

		// We expect c6 to be pushed out of the
		// final list because k is 5
		require.Len(t, s.list, 5)
		require.Equal(t, c1, s.list[0])
		require.Equal(t, c2, s.list[1])
		require.Equal(t, c3, s.list[2])
		require.Equal(t, c4, s.list[3])
		require.Equal(t, c5, s.list[4])
	})

	t.Run("deduplication", func(t *testing.T) {
		s := newShortlist(target, 5)
		c1 := Contact{ID: ParseUint64(1)}
		c2 := Contact{ID: ParseUint64(2)}
		s.Insert(c2, c1, c2, c1, c2, c1, c2, c1)
		require.Len(t, s.list, 2)
		require.Equal(t, c1, s.list[0])
		require.Equal(t, c2, s.list[1])
	})
}

func TestShortlist_Remove(t *testing.T) {
	target := ParseUint64(0)

	t.Run("does not exist", func(t *testing.T) {
		s := newShortlist(target, 5)
		c1 := Contact{ID: ParseUint64(1)}
		c2 := Contact{ID: ParseUint64(2)}
		s.Insert(c1)
		s.Remove(c2)
		require.Len(t, s.list, 1)
		require.Equal(t, c1, s.list[0])
	})

	t.Run("exists", func(t *testing.T) {
		s := newShortlist(target, 5)
		c1 := Contact{ID: ParseUint64(1)}

		// Insert c1
		s.Insert(c1)
		s.SetContacted(c1)
		require.Len(t, s.list, 1)
		require.Equal(t, c1, s.list[0])
		require.Contains(t, s.contacted, c1.ID)

		// Now remove c1
		s.Remove(c1)
		require.Len(t, s.list, 0)
		require.NotContains(t, s.contacted, c1.ID)
	})
}

func TestShortlist_SetContacted(t *testing.T) {
	s := newShortlist(ParseUint64(0), 5)
	c1 := Contact{ID: ParseUint64(1)}
	s.SetContacted(c1)
	require.Contains(t, s.contacted, c1.ID)
}

func TestShortlist_AllContacted(t *testing.T) {
	c1 := Contact{ID: ParseUint64(1)}
	c2 := Contact{ID: ParseUint64(2)}
	c3 := Contact{ID: ParseUint64(3)}
	c4 := Contact{ID: ParseUint64(4)}

	t.Run("stays all not contacted", func(t *testing.T) {
		// Add all four contacts but only
		// have 2 of them contacted, then
		// remove the two contacted ones
		// from the shortlist, the list
		// should be not all contacted
		s := newShortlist(ParseUint64(0), 5)
		s.Insert(c1, c2, c3, c4)
		s.SetContacted(c1, c2)
		s.Remove(c1, c2)
		require.False(t, s.AllContacted())
	})

	t.Run("all contacted", func(t *testing.T) {
		s := newShortlist(ParseUint64(0), 5)
		s.Insert(c1, c2)
		s.SetContacted(c1, c2)
		require.True(t, s.AllContacted())
	})
}

// Utils

func newTestNode(k uint64, port string) *Node {
	return newTestNodeCustom(k, port, DefaultNodeConfig())
}

func newTestNodeCustom(k uint64, port string, c NodeConfig) *Node {
	c.ID = ParseUint64(k)
	c.Host = ""
	p, err := strconv.Atoi(port)
	if err != nil {
		panic(err)
	}
	c.Port = uint16(p)

	n, err := NewNode(c)
	if err != nil {
		panic(err)
	}
	return n
}

// These headers are only needed for inter-node RPCs
func addInuSwarmHeaders(req *http.Request, src Contact, swarm Key) {
	// Add Inu-Src
	data, err := json.Marshal(src)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Inu-Src", string(data))

	// Add the swarm key
	req.Header.Set("Inu-Swarm", swarm.MarshalB32())
}

// Mock RPC client for testing node lookups

type mockLookupRpc struct {
	fn func(dst Contact, k Key) ([]Contact, error)
	fp func(dst Contact, k Key) (findPeersResp, error)
	s  func(c Contact, k Key, p []Peer) error
}

var _ rpc = &mockLookupRpc{}

func (m *mockLookupRpc) Ping(_ Contact) error {
	return nil
}

func (m *mockLookupRpc) Store(c Contact, k Key, p []Peer) error {
	return m.s(c, k, p)
}

func (m *mockLookupRpc) FindNode(dst Contact, k Key) ([]Contact, error) {
	return m.fn(dst, k)
}

func (m *mockLookupRpc) FindPeers(dst Contact, k Key) (findPeersResp, error) {
	return m.fp(dst, k)
}

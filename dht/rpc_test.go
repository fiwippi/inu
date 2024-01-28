package dht

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

var rpcTestSrc = Contact{ID: newKey(2), Address: "fake:80"}

var rpcTestSrcHeader = `{"id":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABA","address":"fake:80"}`

var rpcTestSwarmKey = newKey(56)

func TestRpc_Ping(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		// Create the server and contacts
		ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/rpc/ping", r.URL.EscapedPath())
			require.Equal(t, r.Header.Get("Inu-Src"), rpcTestSrcHeader)
			require.Equal(t, r.Header.Get("Inu-Swarm"), rpcTestSwarmKey.MarshalB32())
		})
		defer ts.Close()

		rpc := newRpc(rpcTestSrc, rpcTestSwarmKey)
		rpc.(*rpcClient).client = ts.Client()
		require.NoError(t, rpc.Ping(dst))
	})

	t.Run("fail", func(t *testing.T) {
		for _, code := range []int{400, 404, 500} {
			ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "GET", r.Method)
				require.Equal(t, "/rpc/ping", r.URL.EscapedPath())
				require.Equal(t, r.Header.Get("Inu-Src"), rpcTestSrcHeader)
				require.Equal(t, r.Header.Get("Inu-Swarm"), rpcTestSwarmKey.MarshalB32())

				w.WriteHeader(code)
			})

			rpc := newRpc(rpcTestSrc, rpcTestSwarmKey)
			rpc.(*rpcClient).client = ts.Client()
			require.Error(t, rpc.Ping(dst))

			ts.Close()
		}
	})
}

func TestRpc_Store(t *testing.T) {
	// Create a pair
	k := newKey(1)
	p := newTestPeer(1)

	t.Run("success", func(t *testing.T) {
		// Create the server and contacts
		ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "POST", r.Method)
			require.Equal(t, "/rpc/store", r.URL.EscapedPath())
			require.Equal(t, r.Header.Get("Inu-Src"), rpcTestSrcHeader)
			require.Equal(t, r.Header.Get("Inu-Swarm"), rpcTestSwarmKey.MarshalB32())

			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			pp := pair{}
			require.NoError(t, json.Unmarshal(body, &pp))
			require.Equal(t, pair{k, p}, pp)
		})
		defer ts.Close()

		// Store the key
		rpc := newRpc(rpcTestSrc, rpcTestSwarmKey)
		rpc.(*rpcClient).client = ts.Client()
		err := rpc.Store(dst, k, p)
		require.NoError(t, err)
	})

	t.Run("fail", func(t *testing.T) {
		for _, code := range []int{400, 404, 500} {
			ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "POST", r.Method)
				require.Equal(t, "/rpc/store", r.URL.EscapedPath())
				require.Equal(t, r.Header.Get("Inu-Src"), rpcTestSrcHeader)
				require.Equal(t, r.Header.Get("Inu-Swarm"), rpcTestSwarmKey.MarshalB32())

				w.WriteHeader(code)
			})

			rpc := newRpc(rpcTestSrc, rpcTestSwarmKey)
			rpc.(*rpcClient).client = ts.Client()
			err := rpc.Store(dst, k, p)
			require.Error(t, err)

			ts.Close()
		}
	})
}

func TestRpc_FindNode(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		// Create the server and contacts
		ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/rpc/find-node/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABA", r.URL.EscapedPath())
			require.Equal(t, r.Header.Get("Inu-Src"), rpcTestSrcHeader)
			require.Equal(t, r.Header.Get("Inu-Swarm"), rpcTestSwarmKey.MarshalB32())

			w.WriteHeader(http.StatusOK)
			_, err := fmt.Fprint(w, `[{"id":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABA","address":"fake:80"}]`)
			require.NoError(t, err)
		})
		defer ts.Close()

		rpc := newRpc(rpcTestSrc, rpcTestSwarmKey)
		rpc.(*rpcClient).client = ts.Client()
		cs, err := rpc.FindNode(dst, newKey(2))
		require.NoError(t, err)
		require.Equal(t, []Contact{{
			ID:      newKey(2),
			Address: "fake:80",
		}}, cs)
	})

	t.Run("fail", func(t *testing.T) {
		for _, code := range []int{400, 404, 500} {
			ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "GET", r.Method)
				require.Equal(t, "/rpc/find-node/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", r.URL.EscapedPath())
				require.Equal(t, r.Header.Get("Inu-Src"), rpcTestSrcHeader)
				require.Equal(t, r.Header.Get("Inu-Swarm"), rpcTestSwarmKey.MarshalB32())

				w.WriteHeader(code)
			})

			rpc := newRpc(rpcTestSrc, rpcTestSwarmKey)
			rpc.(*rpcClient).client = ts.Client()
			rtt, err := rpc.FindNode(dst, Key{})
			require.Error(t, err)
			require.Zero(t, rtt)

			ts.Close()
		}
	})
}

func TestRpc_FindPeers(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		fpr := findPeersResp{
			Found: true,
			Peers: newTestPeer(1),
		}

		// Create the server and contacts
		ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/rpc/find-peers/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABA", r.URL.EscapedPath())
			require.Equal(t, r.Header.Get("Inu-Src"), rpcTestSrcHeader)
			require.Equal(t, r.Header.Get("Inu-Swarm"), rpcTestSwarmKey.MarshalB32())

			data, err := json.Marshal(fpr)
			require.NoError(t, err)
			w.WriteHeader(http.StatusOK)
			_, err = w.Write(data)
			require.NoError(t, err)
		})
		defer ts.Close()

		rpc := newRpc(rpcTestSrc, rpcTestSwarmKey)
		rpc.(*rpcClient).client = ts.Client()
		resp, err := rpc.FindPeers(dst, newKey(2))
		require.NoError(t, err)
		require.Equal(t, fpr, resp)
	})

	t.Run("fail", func(t *testing.T) {
		for _, code := range []int{400, 404, 500} {
			ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "GET", r.Method)
				require.Equal(t, "/rpc/find-peers/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", r.URL.EscapedPath())
				require.Equal(t, r.Header.Get("Inu-Src"), rpcTestSrcHeader)
				require.Equal(t, r.Header.Get("Inu-Swarm"), rpcTestSwarmKey.MarshalB32())

				w.WriteHeader(code)
			})

			rpc := newRpc(rpcTestSrc, rpcTestSwarmKey)
			rpc.(*rpcClient).client = ts.Client()
			rtt, err := rpc.FindPeers(dst, Key{})
			require.Error(t, err)
			require.Zero(t, rtt)

			ts.Close()
		}
	})
}

func newTestServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, Contact) {
	ts := httptest.NewTLSServer(h)
	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	return ts, Contact{Address: u.Host}
}

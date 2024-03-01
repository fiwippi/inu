package dht

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Client Config

func TestClientConfig_JSON(t *testing.T) {
	c1 := DefaultClientConfig()
	data, err := json.MarshalIndent(c1, "", " ")
	require.NoError(t, err)
	fmt.Println(string(data))

	var c2 ClientConfig
	require.NoError(t, json.Unmarshal(data, &c2))
	require.Equal(t, c1, c2)
}

// Client

func TestClient_FindPeers(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		k := ParseUint64(1)
		p := newTestPeer(1)

		// Create the server and contacts
		ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/key/"+k.MarshalB32(), r.URL.EscapedPath())

			data, err := json.Marshal(p)
			require.NoError(t, err)
			w.WriteHeader(http.StatusOK)
			_, err = w.Write(data)
			require.NoError(t, err)
		})
		defer ts.Close()

		dhtC := NewClient(ClientConfig{Port: 3000, Nodes: []string{dst.Address}})
		dhtC.client = ts.Client()

		// Verify peers
		ps, err := dhtC.FindPeers(k)
		require.NoError(t, err)
		require.Equal(t, p, ps)
	})

	t.Run("fail", func(t *testing.T) {
		t.Run("response codes", func(t *testing.T) {
			k := ParseUint64(1)

			for _, code := range []int{400, 404, 500} {
				ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "GET", r.Method)
					require.Equal(t, "/key/"+k.MarshalB32(), r.URL.EscapedPath())
					w.WriteHeader(code)
				})

				dhtC := NewClient(ClientConfig{Port: 3000, Nodes: []string{dst.Address}})
				dhtC.client = ts.Client()
				ps, err := dhtC.FindPeers(k)
				require.Error(t, err)
				require.Nil(t, ps)

				ts.Close()
			}

		})

		t.Run("bad get response", func(t *testing.T) {
			k := ParseUint64(1)

			// Create the server and contacts
			ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "GET", r.Method)
				require.Equal(t, "/key/"+k.MarshalB32(), r.URL.EscapedPath())

				w.WriteHeader(http.StatusOK)
				_, err := w.Write([]byte("asxaasd"))
				require.NoError(t, err)
			})
			defer ts.Close()

			dhtC := NewClient(ClientConfig{Port: 3000, Nodes: []string{dst.Address}})
			dhtC.client = ts.Client()

			ps, err := dhtC.FindPeers(k)
			require.Error(t, err)
			require.Nil(t, ps)
		})
	})
}

func TestClient_PutKey(t *testing.T) {
	k := ParseUint64(1)
	uploadK := ParseUint64(2)

	t.Run("success", func(t *testing.T) {
		t.Run("has upload key", func(t *testing.T) {
			ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "PUT", r.Method)
				require.Equal(t, "/key/"+k.MarshalB32(), r.URL.EscapedPath())
				require.Equal(t, r.Header.Get("Inu-Upload"), uploadK.MarshalB32())

				data, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, `2000`, string(data))

				w.WriteHeader(http.StatusOK)
			})
			defer ts.Close()

			dhtC := NewClient(ClientConfig{
				Port:      2000,
				Nodes:     []string{dst.Address},
				UploadKey: uploadK,
			})
			dhtC.client = ts.Client()

			err := dhtC.PutKey(k)
			require.NoError(t, err)
		})

		t.Run("no upload key", func(t *testing.T) {
			// Create the server and contacts
			ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "PUT", r.Method)
				require.Equal(t, "/key/"+k.MarshalB32(), r.URL.EscapedPath())
				require.Len(t, r.Header.Get("Inu-Upload"), 0)

				data, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var port uint16
				require.NoError(t, json.Unmarshal(data, &port))
				require.Equal(t, uint16(3000), port)

				w.WriteHeader(http.StatusOK)
			})
			defer ts.Close()

			dhtC := NewClient(ClientConfig{Port: 3000, Nodes: []string{dst.Address}})
			dhtC.client = ts.Client()

			require.NoError(t, dhtC.PutKey(k))
		})
	})

	t.Run("fail", func(t *testing.T) {
		k := ParseUint64(1)

		for _, code := range []int{400, 404, 500} {
			ts, dst := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "GET", r.Method)
				require.Equal(t, "/key/"+k.MarshalB32(), r.URL.EscapedPath())
				w.WriteHeader(code)
			})

			dhtC := NewClient(ClientConfig{Port: 3000, Nodes: []string{dst.Address}})
			dhtC.client = ts.Client()
			ps, err := dhtC.FindPeers(k)
			require.Error(t, err)
			require.Nil(t, ps)

			ts.Close()
		}
	})
}

func TestIntegrationClient(t *testing.T) {
	uploadKey := ParseUint64(56)
	client := NewClient(ClientConfig{
		Port:      60,
		Nodes:     []string{"127.0.0.1:3000"}, // Node A
		UploadKey: uploadKey,
	})
	localhost := netip.MustParseAddr("127.0.0.1")

	// Initial node A, ID = 1
	A := newTestNode(pow2(0), "3000")
	A.config.UploadKey = uploadKey
	require.NoError(t, A.UpdateASN(netip.PrefixFrom(localhost, 1), 1))
	require.NoError(t, A.Start())
	defer A.Stop()
	time.Sleep(1 * time.Second) // Wait for node to start

	// Add node B, ID = 2
	B := newTestNode(pow2(1), "3001")
	require.NoError(t, B.Start())
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

	k := ParseUint64(5)

	t.Run("authed upload to A", func(t *testing.T) {
		require.NoError(t, client.PutKey(k))

		// Verify peer exists on A and B
		peersA, err := A.peerStore.Get(k)
		require.NoError(t, err)
		require.Len(t, peersA, 1)
		require.Equal(t, localhost, peersA[0].IP)
		require.Equal(t, client.config.Port, peersA[0].Port)
		require.WithinDuration(t, time.Now(), peersA[0].Published, 1*time.Second)
		require.Equal(t, 1, peersA[0].ASN) // The ASN for localhost on A is 1

		peersB, err := B.peerStore.Get(k)
		require.NoError(t, err)
		require.Equal(t, peersA, peersB)
	})

	t.Run("get peers for key", func(t *testing.T) {
		ps, err := client.FindPeers(k)
		require.NoError(t, err)
		require.Len(t, ps, 1)
		require.Equal(t, localhost, ps[0].IP)
		require.Equal(t, client.config.Port, ps[0].Port)
		require.WithinDuration(t, time.Now(), ps[0].Published, 1*time.Second)
		require.Equal(t, 1, ps[0].ASN) // The ASN for localhost on A is 1
	})
}

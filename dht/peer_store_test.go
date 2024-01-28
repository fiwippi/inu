package dht

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var peerStoreTestDefaultExpiry = 24 * time.Hour

// Store

func TestPeerStore_Put(t *testing.T) {
	s := newPeerStore(peerStoreTestDefaultExpiry)

	k := newKey(1)

	// Peer set created if it does not exist
	ps1 := newTestPeer(1)
	s.Put(k, ps1)
	require.Contains(t, s.data, k)
	require.Contains(t, s.data[k], ps1[0])

	// Peer set updated and not recreated if key updated again
	ps2 := newTestPeer(2)
	s.Put(k, ps2)
	require.Contains(t, s.data, k)
	require.Len(t, s.data[k], 2)
	for _, ps := range [][]Peer{ps1, ps2} {
		for _, p := range ps {
			require.Contains(t, s.data[k], p)
		}
	}
}

func TestPeerStore_Delete(t *testing.T) {
	s := newPeerStore(peerStoreTestDefaultExpiry)

	k := newKey(1)

	// Successful put
	ps := newTestPeer(1)
	s.Put(k, ps)
	require.Contains(t, s.data, k)
	require.Contains(t, s.data[k], ps[0])

	// Delete wipes it from the store
	s.Delete(k)
	require.NotContains(t, s.data, k)
}

func TestPeerStore_Get(t *testing.T) {
	t.Run("key not found", func(t *testing.T) {
		s := newPeerStore(peerStoreTestDefaultExpiry)
		ps, err := s.Get(newKey(1))
		require.ErrorContains(t, err, "key not found")
		require.Nil(t, ps)
	})

	t.Run("all peers dead", func(t *testing.T) {
		s := newPeerStore(peerStoreTestDefaultExpiry)
		k := newKey(1)

		s.Put(k, []Peer{{ASN: 1}, {ASN: 2}, {ASN: 3}, {ASN: 4}})
		require.Contains(t, s.data, k)
		require.Len(t, s.data[k], 4)

		// Get should be unsuccessful
		ps, err := s.Get(k)
		require.ErrorContains(t, err, "all peers dead")
		require.Nil(t, ps)

		// Since all peers are dead the key should
		// be deleted from the store
		require.NotContains(t, s.data, k)
	})

	t.Run("some peers dead", func(t *testing.T) {
		s := newPeerStore(peerStoreTestDefaultExpiry)
		k := newKey(1)

		dead1, dead2 := Peer{ASN: 1}, Peer{ASN: 2}
		alive1 := Peer{ASN: 3, Published: time.Now().UTC()}
		alive2 := Peer{ASN: 4, Published: time.Now().UTC()}
		s.Put(k, []Peer{dead1, dead2, alive1, alive2})
		require.Contains(t, s.data, k)
		require.Len(t, s.data[k], 4)

		// Get should be unsuccessful
		ps, err := s.Get(k)
		require.Nil(t, err)
		require.Len(t, ps, 2)
		require.Contains(t, ps, alive1)
		require.Contains(t, ps, alive2)

		// The dead peers should be deleted from the store
		// and alive peers should still exist within it
		require.Contains(t, s.data, k)
		require.Len(t, s.data[k], 2)
		require.Contains(t, s.data[k], alive1)
		require.Contains(t, s.data[k], alive2)
	})

	t.Run("no peers dead", func(t *testing.T) {
		s := newPeerStore(peerStoreTestDefaultExpiry)
		k := newKey(1)
		ps := []Peer{{ASN: 1, Published: time.Now().UTC()}}

		s.Put(k, ps)
		require.Contains(t, s.data, k)
		require.Len(t, s.data[k], 1)

		pps, err := s.Get(k)
		require.Nil(t, err)
		require.Equal(t, ps, pps)
	})
}

func TestPeerStore_GetAll(t *testing.T) {
	s := newPeerStore(peerStoreTestDefaultExpiry)

	valid := time.Now()
	expired := valid.Add(-25 * time.Hour)

	pairs := []pair{
		{K: newKey(1), P: []Peer{{ASN: 1, Published: valid}}},
		{K: newKey(2), P: []Peer{{ASN: 2, Published: expired}}},
		{K: newKey(3), P: []Peer{{ASN: 3, Published: valid}}},
		{K: newKey(4), P: []Peer{{ASN: 4, Published: expired}}},
		{K: newKey(5), P: []Peer{{ASN: 5, Published: valid}}},
		{K: newKey(6), P: []Peer{{ASN: 6, Published: expired}}},
		{K: newKey(7), P: []Peer{{ASN: 7, Published: valid}}},
	}
	for _, p := range pairs {
		s.Put(p.K, p.P)
	}

	// Verify we have the correct pairs returned
	validPairs := s.GetAll()
	for _, p := range pairs {
		if p.P[0].Published.Equal(valid) {
			require.Contains(t, validPairs, p)
		} else {
			require.NotContains(t, validPairs, p)
		}
	}

	// The invalid pairs should also be deleted from the store
	for _, p := range pairs {
		if p.P[0].Published.Equal(valid) {
			require.Contains(t, s.data, p.K)
		} else {
			require.NotContains(t, s.data, p.K)
		}
	}
}

// Peer util

func newTestPeer(ports ...uint16) []Peer {
	ps := make([]Peer, len(ports))
	for i, p := range ports {
		ps[i] = Peer{Port: p, Published: time.Now().UTC()}
	}
	return ps
}

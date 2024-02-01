package dht

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBucket_UpdateContact(t *testing.T) {
	mRpc := &mockPingRpc{}
	self := ParseUint64(4)
	b := newBucket(2, ParseUint64(0), ParseUint64(5))

	// Define the contacts
	c1 := Contact{ID: ParseUint64(1), Address: "1"}
	c1a := Contact{ID: ParseUint64(1), Address: "11"}
	c2 := Contact{ID: ParseUint64(2), Address: "2"}

	// Able to insert two values into empty bucket
	t.Run("successful insert", func(t *testing.T) {
		// Insert c1
		require.False(t, b.UpdateContact(c1, self, mRpc))
		require.Equal(t, 1, b.contacts.Len())

		// Insert c2
		require.False(t, b.UpdateContact(c2, self, mRpc))
		require.Equal(t, 2, b.contacts.Len())
		require.Equal(t, c1, b.element(0))
		require.Equal(t, c2, b.element(1))

		// Verify contacts also exist in the map
		require.Contains(t, b.elements, c1.ID)
		require.Contains(t, b.elements, c2.ID)
	})

	// Overwrites old entry and moves it to the front
	t.Run("correct overwrite", func(t *testing.T) {
		// Insert c1a
		require.False(t, b.UpdateContact(c1a, self, mRpc))

		// It replaces c1 in the list and in the map
		require.Equal(t, 2, b.contacts.Len())
		require.Equal(t, c2, b.element(0))
		require.Equal(t, c1a, b.element(1))
		require.Contains(t, b.elements, c1a.ID)
		require.Contains(t, b.elements, c2.ID)
	})

	// Rejects new entry on successful ping, moves
	// the least recently seen entry to the front
	t.Run("ping success (no split)", func(t *testing.T) {
		// We don't split because self is out of range
		mRpc.fail = false
		c3 := Contact{ID: ParseUint64(4), Address: "4"}
		require.False(t, b.UpdateContact(c3, ParseUint64(10), mRpc))

		require.Equal(t, c1a, b.element(0))
		require.Equal(t, c2, b.element(1))

		require.NotContains(t, b.elements, c3.ID)
		require.Contains(t, b.elements, c1a.ID)
		require.Contains(t, b.elements, c2.ID)
	})

	// Inserts new entry on unsuccessful ping and
	// discards least recently seen from the front
	t.Run("ping fail (no split)", func(t *testing.T) {
		// We don't split because self is out of range
		mRpc.fail = true
		c4 := Contact{ID: ParseUint64(4), Address: "4"}
		require.False(t, b.UpdateContact(c4, ParseUint64(10), mRpc))

		require.Equal(t, 2, b.contacts.Len())
		require.Equal(t, c2, b.element(0))
		require.Equal(t, c4, b.element(1))

		require.NotContains(t, b.elements, c1a.ID)
		require.Contains(t, b.elements, c2.ID)
		require.Contains(t, b.elements, c4.ID)
	})

	t.Run("should split", func(t *testing.T) {
		c3 := Contact{ID: ParseUint64(3), Address: "3"}
		require.True(t, b.UpdateContact(c3, self, mRpc))
	})
}

func TestBucket_Split(t *testing.T) {
	t.Run("split", func(t *testing.T) {
		mRpc := &mockPingRpc{}
		self := ParseUint64(5)
		b := newBucket(2, ParseUint64(0), ParseUint64(10))

		// Define the contacts
		c1 := Contact{ID: ParseUint64(5)}
		c2 := Contact{ID: ParseUint64(6)}

		// Insert the two contacts
		require.False(t, b.UpdateContact(c1, self, mRpc))
		require.False(t, b.UpdateContact(c2, self, mRpc))

		// Split the bucket
		x, y := b.Split(self)
		require.Len(t, x.elements, 1)
		require.Equal(t, x.low, ParseUint64(0))
		require.Equal(t, x.high, ParseUint64(5))
		require.Len(t, y.elements, 1)
		require.Equal(t, y.low, ParseUint64(6))
		require.Equal(t, y.high, ParseUint64(10))

		// Lower bucket should have 5, and upper 6
		require.Contains(t, x.elements, c1.ID)
		require.Contains(t, y.elements, c2.ID)
	})

	t.Run("no overlap in keyspace", func(t *testing.T) {
		// Create a bucket for the whole keyspace and split it
		high := fromBigInt(new(big.Int).Exp(big.NewInt(2), big.NewInt(255), nil))
		b := newBucket(1, ParseUint64(0), high)
		x, y := b.Split(ParseUint64(5))

		// The midpoints of each bucket should not overlap
		// (i.e. y.low - x.high == 1, exactly one apart)
		xHigh := new(big.Int).SetBytes(x.high[:])
		yLow := new(big.Int).SetBytes(y.low[:])
		diff := new(big.Int).Sub(yLow, xHigh)
		require.Equal(t, big.NewInt(1), diff)
	})
}

func TestRoutingTable_UpdateContact(t *testing.T) {
	// Create the routing table
	self := Contact{ID: ParseUint64(5)}
	rt := newRoutingTable(self, 2)

	// Initialise the mock RPC
	mRpc := &mockPingRpc{
		fail: false,
	}

	c1 := Contact{ID: ParseUint64(1)}
	c2 := Contact{ID: ParseUint64(2)}
	c3 := Contact{ID: fromBigInt(new(big.Int).Exp(big.NewInt(2), big.NewInt(250), nil))}

	// Create contacts and update them in the routing table
	t.Run("one", func(t *testing.T) {
		rt.UpdateContact(c1, mRpc)
		require.Len(t, rt.buckets, 1)
		require.Equal(t, rt.buckets[0].element(0), c1)
	})

	t.Run("two", func(t *testing.T) {
		rt.UpdateContact(c2, mRpc)
		require.Len(t, rt.buckets, 1)
		require.Equal(t, rt.buckets[0].element(0), c1)
		require.Equal(t, rt.buckets[0].element(1), c2)
	})

	t.Run("three (split)", func(t *testing.T) {
		rt.UpdateContact(c3, mRpc)
		require.Len(t, rt.buckets, 7)
		require.Contains(t, rt.buckets[0].elements, c1.ID)
		require.Contains(t, rt.buckets[0].elements, c2.ID)
		require.Contains(t, rt.buckets[1].elements, c3.ID)
	})
}

func TestRoutingTable_FindClosestNodes(t *testing.T) {
	// Create the routing table
	origin := Contact{ID: ParseUint64(255)}
	rt := newRoutingTable(origin, 20)

	// Initialise the mock RPC
	mRpc := &mockPingRpc{
		fail: false,
	}

	// Generate 20 contacts in sorted order
	cs := genContacts(20)
	// Insert these into the routing table
	for _, c := range cs {
		rt.UpdateContact(c, mRpc)
	}

	// Our closest nodes to the origin should be
	// the sorted order of these contacts
	start := ParseUint64(0)
	target := ParseUint64(1)
	closest := rt.FindClosestNodes(&start, &target)
	require.Equal(t, cs, closest)
}

// Utils for buckets/routing table

func (b *bucket) element(i int) Contact {
	count := 0
	for e := b.contacts.Front(); e != nil; e = e.Next() {
		if i == count {
			return e.Value.(Contact)
		}
		count++
	}

	panic("invalid index " + string(rune(i)))
}

func genContacts(n int) []Contact {
	cs := make([]Contact, n)
	for i := 1; i <= n; i++ {
		cs[i-1] = Contact{ID: ParseUint64(pow2(i))}
	}

	return cs
}

// Mock RPC client for testing ping

type mockPingRpc struct {
	fail bool
}

var _ rpc = &mockPingRpc{}

func (m *mockPingRpc) Ping(_ Contact) error {
	if m.fail {
		return fmt.Errorf("fail")
	}
	return nil
}

func (m *mockPingRpc) Store(_ Contact, _ Key, _ []Peer) error {
	return nil
}

func (m *mockPingRpc) FindNode(_ Contact, _ Key) ([]Contact, error) {
	return []Contact{}, nil
}

func (m *mockPingRpc) FindPeers(_ Contact, _ Key) (findPeersResp, error) {
	return findPeersResp{}, nil
}

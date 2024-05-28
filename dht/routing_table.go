package dht

import (
	"container/list"
	"log/slog"
	"math"
	"math/big"
	"slices"
	"sync"
	"time"
)

// Contact

type Contact struct {
	ID      Key    `json:"id"`
	Address string `json:"address"`
}

// Bucket

type bucket struct {
	// Contacts
	k        int
	contacts *list.List
	elements map[Key]*list.Element

	// Refresh info
	lastUpdated time.Time

	// Bucket range
	low  Key
	high Key
}

func newBucket(k int, low Key, high Key) bucket {
	if k <= 0 {
		panic("k must be above 0")
	}

	return bucket{
		k:           k,
		contacts:    list.New(),
		elements:    make(map[Key]*list.Element),
		lastUpdated: time.Now(),
		low:         low,
		high:        high,
	}
}

func (b *bucket) inRange(k Key) bool {
	n := new(big.Int).SetBytes(k[:])
	lCmp := n.Cmp(new(big.Int).SetBytes(b.low[:]))
	hCmp := n.Cmp(new(big.Int).SetBytes(b.high[:]))
	return (lCmp == 1 || lCmp == 0) && (hCmp == -1 || hCmp == 0)
}

func (b *bucket) UpdateContact(c Contact, self Key, rpc rpc) bool {
	// Update the time the bucket was last updated
	// (Used so we know which buckets to refresh)
	b.lastUpdated = time.Now()

	// If the contact already exists then
	// move it to the tail
	e, found := b.elements[c.ID]
	if found {
		b.contacts.Remove(e)
		e := b.contacts.PushBack(c)
		b.elements[c.ID] = e
		return false
	}

	// If the bucket has fewer than k entries
	// then insert the contact at the tail
	if b.contacts.Len() < b.k {
		e := b.contacts.PushBack(c)
		b.elements[c.ID] = e
		return false
	}

	// If the bucket is full and the DHT node's
	// key falls in the range of the bucket then
	// it's split
	if b.inRange(self) {
		return true
	}

	// If full then ping the least recently seen node
	dst := b.contacts.Front().Value.(Contact)
	err := rpc.Ping(dst)
	if err != nil {
		slog.Error("Failed to ping the lrs node", slog.Any("err", err))

		// Discard the LRS node and insert the sender
		// at the tail
		front := b.contacts.Front()
		b.contacts.Remove(front)
		delete(b.elements, front.Value.(Contact).ID)

		// Insert the contact at the tail
		e := b.contacts.PushBack(c)
		b.elements[c.ID] = e

		return false
	}

	// Move the LRS node to the tail and discard
	// the new contact
	b.contacts.MoveToBack(b.contacts.Front())
	return false
}

func (b *bucket) Split(self Key) (bucket, bucket) {
	// Calculate the midpoint in the keyspace to split at
	l := new(big.Int).SetBytes(b.low[:])
	h := new(big.Int).SetBytes(b.high[:])
	mid := new(big.Int).Div(l.Add(l, h), big.NewInt(2))

	// Represent this midpoint as a key
	midX := fromBigInt(mid)
	midY := fromBigInt(new(big.Int).Add(mid, big.NewInt(1)))

	// Create the two new buckets
	x := newBucket(b.k, b.low, midX)
	y := newBucket(b.k, midY, b.high)

	// Add the nodes to the correct bucket
	for _, e := range b.elements {
		c := e.Value.(Contact)
		k := new(big.Int).SetBytes(c.ID[:])

		// We can supply a nil RPC because we
		// are guaranteed to have space in the
		// bucket
		kCmp := k.Cmp(mid)
		if kCmp == -1 || kCmp == 0 { // x < y || x == y
			x.UpdateContact(c, self, nil)
		} else {
			y.UpdateContact(c, self, nil)
		}
	}

	return x, y
}

// Routing Table

type routingTable struct {
	sync.RWMutex

	k       int
	self    Contact
	buckets []bucket
}

func newRoutingTable(self Contact, k int) *routingTable {
	low := Key{}
	high := Key{}
	for i := range k {
		high[i] = math.MaxUint8
	}

	return &routingTable{
		k:       k,
		self:    self,
		buckets: []bucket{newBucket(k, low, high)},
	}
}

func (rt *routingTable) bucket(k Key) int {
	for i, b := range rt.buckets {
		if b.inRange(k) {
			return i
		}
	}

	panic("bucket not found")
}

func (rt *routingTable) updateContact(c Contact, rpc rpc) {
	// Find the bucket corresponding to the contact
	i := rt.bucket(c.ID)

	// Update the contact's place in the bucket
	split := rt.buckets[i].UpdateContact(c, rt.self.ID, rpc)
	if !split {
		return
	}

	// Perform splitting if the bucket is full
	//   1. Split the buckets
	//   2. Remove the old bucket with the lower bound
	//   3. Insert the higher bound after the lower bound

	// 1 + 2
	x, y := rt.buckets[i].Split(rt.self.ID)
	rt.buckets[i] = x

	// 3
	rt.buckets = append(rt.buckets, bucket{})
	copy(rt.buckets[i+2:], rt.buckets[i+1:])
	rt.buckets[i+1] = y

	// We have to add the contact again
	rt.updateContact(c, rpc)
}

func (rt *routingTable) UpdateContact(c Contact, rpc rpc) {
	rt.Lock()
	defer rt.Unlock()

	rt.updateContact(c, rpc)
}

func (rt *routingTable) FindClosestNodes(k, exclude *Key) []Contact {
	rt.RLock()
	defer rt.RUnlock()

	// Linear scan the whole table and calculate the
	// distance from the target contact to each other
	// contact
	cs := make([]Contact, 0)
	for _, b := range rt.buckets {
		for _, e := range b.elements {
			c := e.Value.(Contact)

			// Always ignore the excluded node
			if c.ID == *exclude {
				continue
			}

			cs = append(cs, c)
		}
	}

	// Sort by closest distance
	slices.SortFunc(cs, func(a, b Contact) int {
		d1 := k.xor(a.ID)
		d2 := k.xor(b.ID)
		return d1.cmp(d2)
	})

	// If we have more than k entries then shrink it down
	if len(cs) > rt.k {
		cs = cs[:rt.k]
	}

	return cs
}

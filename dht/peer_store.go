package dht

import (
	"fmt"
	"net/netip"
	"sync"
	"time"
)

type Peer struct {
	IP        netip.Addr `json:"ip"`
	Port      uint16     `json:"port"`
	ASN       int        `json:"asn"`
	Published time.Time  `json:"published"`
}

type peerStore struct {
	sync.Mutex

	data   map[Key]map[Peer]struct{}
	expiry time.Duration
}

func newPeerStore(expiry time.Duration) *peerStore {
	return &peerStore{
		data:   make(map[Key]map[Peer]struct{}),
		expiry: expiry,
	}
}

func (s *peerStore) get(k Key) ([]Peer, error) {
	pmap, found := s.data[k]
	if !found {
		return nil, fmt.Errorf("key not found")
	}

	// Remove all expired contacts
	for p := range pmap {
		expired := p.Published.Add(s.expiry)
		if time.Now().After(expired) {
			delete(pmap, p)
		}
	}
	if len(pmap) == 0 {
		delete(s.data, k)
		return nil, fmt.Errorf("all peers dead")
	}

	ps := make([]Peer, 0)
	for p := range pmap {
		ps = append(ps, p)
	}
	return ps, nil
}

func (s *peerStore) Get(k Key) ([]Peer, error) {
	s.Lock()
	defer s.Unlock()

	return s.get(k)
}

func (s *peerStore) Put(k Key, ps []Peer) {
	s.Lock()
	defer s.Unlock()

	_, found := s.data[k]
	if !found {
		s.data[k] = make(map[Peer]struct{})
	}

	for _, p := range ps {
		s.data[k][p] = struct{}{}
	}
}

func (s *peerStore) Delete(k Key) {
	s.Lock()
	defer s.Unlock()

	delete(s.data, k)
}

func (s *peerStore) GetAll() []pair {
	s.Lock()
	defer s.Unlock()

	// Collect all keys in our store
	keys := make([]Key, 0)
	for k := range s.data {
		keys = append(keys, k)
	}

	// Now get them; the get method already
	// handles the deletion of keys if the
	// stored peers are dead
	pairs := make([]pair, 0)
	for _, k := range keys {
		ps, err := s.get(k)
		if err == nil {
			pairs = append(pairs, pair{k, ps})
		}
	}

	return pairs
}

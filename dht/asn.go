package dht

import (
	"fmt"
	"net/netip"
)

// Trie Node

type trieNode struct {
	children [2]*trieNode

	asn int // 0 if non-terminal node
}

func (n *trieNode) Insert(p netip.Prefix, asn int) error {
	if !p.Addr().Is4() {
		return fmt.Errorf("prefix must be ipv4")
	}

	x := n

	// Iterate down the trie for each bit in the CIDR range
	buf := p.Addr().As4()
	for i := 0; i < p.Bits(); i++ {
		b := bitAt(buf, i)
		if x.children[b] == nil {
			x.children[b] = new(trieNode)
		}
		x = x.children[b]
	}

	// Set the ASN at the resulting node
	x.asn = asn

	return nil
}

func (n *trieNode) Find(p netip.Prefix) (int, bool) {
	x := n

	// Iterate down the trie for each bit in the CIDR range
	buf := p.Addr().As4()
	for i := 0; i < p.Bits(); i++ {
		b := bitAt(buf, i)
		if x.children[b] == nil {
			return 0, false
		}
		x = x.children[b]
	}

	return x.asn, true
}

func (n *trieNode) Match(a netip.Addr) (asn int, found bool) {
	x := n

	i := 0
	buf := a.As4()
	for {
		// Check if the node we are at is terminal
		if x.asn != 0 {
			asn = x.asn
			found = true
		}

		// Otherwise continue traversing the trie
		// (until we reach a leaf)
		b := bitAt(buf, i)
		if x.children[b] == nil {
			return
		}
		x = x.children[b]

		i += 1
	}
}

// Bit manipulation

func bitAt(buf [4]byte, i int) uint8 {
	return (buf[i/8] >> (7 - (i % 8))) & 1
}

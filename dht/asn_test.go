package dht

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrie(t *testing.T) {
	tr := new(trieNode)

	a := netip.MustParseAddr("192.168.20.19")

	t.Run("initial match to least specific", func(t *testing.T) {
		p := netip.MustParsePrefix("192.168.0.0/16")
		require.NoError(t, tr.Insert(p, 2))
		asn, found := tr.Match(a)
		require.True(t, found)
		require.Equal(t, 2, asn)
	})

	t.Run("second match to more specific", func(t *testing.T) {
		p := netip.MustParsePrefix("192.168.20.16/28")
		require.NoError(t, tr.Insert(p, 1))
		asn, found := tr.Match(a)
		require.True(t, found)
		require.Equal(t, 1, asn)
	})

	t.Run("no match", func(t *testing.T) {
		asn, found := tr.Match(netip.MustParseAddr("1.1.1.1"))
		require.False(t, found)
		require.Equal(t, 0, asn)
	})
}

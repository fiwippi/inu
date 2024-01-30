package dht

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKey_MarshalB32(t *testing.T) {
	s := newKey(0).MarshalB32()
	require.Equal(t, `AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA`, s)

	s = newKey(pow2(23) * 31 / 5).MarshalB32()
	require.Equal(t, `AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAYZTGMQ`, s)
}

func TestKey_UnmarshalB32(t *testing.T) {
	k := Key{}
	require.NoError(t, k.UnmarshalB32("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAYZTGMQ"))
	require.Equal(t, newKey(pow2(23)*31/5), k)
}

func TestKey_ParseCID(t *testing.T) {
	k, err := ParseCID("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAYZTGMQ")
	require.NoError(t, err)
	require.Equal(t, newKey(pow2(23)*31/5), k)
}

func newKey(n uint64) Key {
	// Encode the number to binary
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, n)

	// Copy this onto the key
	k := Key{}
	copy(k[keyLen-len(buf):], buf[:])
	return k
}

func pow2(n int) uint64 {
	if n > 63 {
		panic("n cannot be larger than 63")
	}

	return uint64(math.Pow(2, float64(n)))
}

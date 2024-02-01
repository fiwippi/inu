package dht

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKey_MarshalB32(t *testing.T) {
	s := ParseUint64(0).MarshalB32()
	require.Equal(t, `AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA`, s)

	s = ParseUint64(pow2(23) * 31 / 5).MarshalB32()
	require.Equal(t, `AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAYZTGMQ`, s)
}

func TestKey_UnmarshalB32(t *testing.T) {
	k := Key{}
	require.NoError(t, k.UnmarshalB32("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAYZTGMQ"))
	require.Equal(t, ParseUint64(pow2(23)*31/5), k)
}

func TestKey_ParseCID(t *testing.T) {
	k, err := ParseCID("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAYZTGMQ")
	require.NoError(t, err)
	require.Equal(t, ParseUint64(pow2(23)*31/5), k)
}

func pow2(n int) uint64 {
	if n > 63 {
		panic("n cannot be larger than 63")
	}

	return uint64(math.Pow(2, float64(n)))
}

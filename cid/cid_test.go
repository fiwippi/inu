package cid

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCID(t *testing.T) {
	require.Equal(t, CID("JP2RELZUIVKMKO66F25YZUVX4PIWACWWGHBYLJOXZTRDY54FIWNA"), New([]byte{1}))
}

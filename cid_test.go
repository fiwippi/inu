package inu

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCID(t *testing.T) {
	require.Equal(t, "JP2RELZUIVKMKO66F25YZUVX4PIWACWWGHBYLJOXZTRDY54FIWNA", NewCID([]byte{1}))
}

package store

import (
	"testing"

	"github.com/stretchr/testify/require"

	"inu"
)

func TestNewBlock(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		b, err := NewBlock([]byte("a"))
		require.NoError(t, err)
		require.Equal(t, inu.CID("ZKLYCEWKDO64V6WCGGZZUI64JWTYN37YCR6E44VZQB3YLL7OJC5Q"), b.CID)
		require.Equal(t, []byte("a"), b.Data)
	})

	t.Run("too large", func(t *testing.T) {
		_, err := NewBlock(make([]byte, MaxBlockSize+1))
		require.Error(t, err)
	})
}

func TestBlock_Valid(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		b, err := NewBlock([]byte("a"))
		require.NoError(t, err)
		require.True(t, b.Valid())
	})

	t.Run("invalid", func(t *testing.T) {
		b := Block{
			CID:  "invalid",
			Data: []byte("a"),
		}
		require.False(t, b.Valid())
	})
}

package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore(t *testing.T) {
	s := NewStore("file::memory:")
	b, err := NewBlock([]byte("a"))
	require.NoError(t, err)

	t.Run("put", func(t *testing.T) {
		require.NoError(t, s.Put(b))
	})

	t.Run("full", func(t *testing.T) {
		n, err := s.Size()
		require.NoError(t, err)
		require.Equal(t, uint(1), n)
	})

	t.Run("get", func(t *testing.T) {
		bb, err := s.Get(b.CID)
		require.NoError(t, err)
		require.Equal(t, b, bb)
	})

	t.Run("delete", func(t *testing.T) {
		require.NoError(t, s.Delete(b.CID))
		bb, err := s.Get(b.CID)
		require.Error(t, err)
		require.Equal(t, Block{}, bb)
	})

	t.Run("empty", func(t *testing.T) {
		n, err := s.Size()
		require.NoError(t, err)
		require.Equal(t, uint(0), n)
	})

	t.Run("close", func(t *testing.T) {
		require.NoError(t, s.Close())
	})
}

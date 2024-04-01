package fs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"inu/cid"
	"inu/merkle"
	"inu/store"
)

func TestDeduplication(t *testing.T) {
	fs := NewFS(store.InMemory)
	cat, err := os.ReadFile("../test/redcat.jpg")
	require.NoError(t, err)

	t.Run("store one chunk", func(t *testing.T) {
		// Create node
		firstChunk := cat[:ChunkSize]
		n, err := fs.AddBytes(firstChunk)
		require.NoError(t, err)
		require.Len(t, n.Links(), 0)

		// Get node
		b, err := fs.store.Get(n.Block().CID)
		require.NoError(t, err)
		m, err := merkle.ParseBlock(b)
		require.NoError(t, err)
		require.Equal(t, n, m)

		// Read node
		v, err := fs.ReadBytes(&m)
		require.NoError(t, err)
		require.Equal(t, firstChunk, v)
	})

	t.Run("store multiple chunks", func(t *testing.T) {
		// Create node
		n, err := fs.AddBytes(cat)
		require.NoError(t, err)
		require.Len(t, n.Links(), 5)

		// Get node
		b, err := fs.store.Get(n.Block().CID)
		require.NoError(t, err)
		m, err := merkle.ParseBlock(b)
		require.NoError(t, err)
		require.Equal(t, n, m)

		// Read node
		v, err := fs.ReadBytes(&m)
		require.NoError(t, err)
		require.Equal(t, cat, v)

		// Our store should have 6 nodes,
		size, err := fs.store.Size()
		require.NoError(t, err)
		require.Equal(t, uint(6), size)
	})
}

func TestResolution(t *testing.T) {
	// Store the assets in the test directory into the store
	fs := NewFS(store.InMemory)
	n, rs, err := fs.AddPath("../test")
	require.NoError(t, err)
	require.Len(t, rs, 2)
	require.Equal(t, cid.CID(`5Q7YC5E2QPBRRSZAERGNYUXHASWHO7QRNOPRXDNPNOTZYUK6IKWQ`), n.Block().CID)

	// Resolve to the root node for redcat.jpg
	m, err := fs.ResolvePath(string(n.Block().CID) + "/redcat.jpg")
	require.NoError(t, err)

	// Read this root node and ensure it equals
	// to redcat.jpg
	cat, err := os.ReadFile("../test/redcat.jpg")
	require.NoError(t, err)
	data, err := fs.ReadBytes(&m)
	require.NoError(t, err)
	require.Equal(t, cat, data)
}

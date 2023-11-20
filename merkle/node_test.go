package merkle

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNodeBuilder(t *testing.T) {
	a, err := NewNodeBuilder().SetData([]byte("a")).Build()
	require.NoError(t, err)
	b, err := NewNodeBuilder().SetData([]byte("b")).AddLink("a", &a).Build()
	require.NoError(t, err)

	t.Run("node correct", func(t *testing.T) {
		// A
		require.Equal(t, []byte("a"), a.attr.Data)
		require.Len(t, a.attr.Links, 0)

		// B
		require.Equal(t, []byte("b"), b.attr.Data)
		require.Len(t, b.attr.Links, 1)
		require.Equal(t, "a", b.attr.Links[0].Name)
		require.Equal(t, a.block.CID, b.attr.Links[0].CID)
	})

	t.Run("encoding correct", func(t *testing.T) {
		// A
		require.Equal(t, `{"links":[],"data":"YQ=="}`, string(a.block.Data))

		// B
		require.Equal(t, `{"links":[{"name":"a","cid":"RMD2PZNGR24S4LR37N4TB32U362S2RVISJ3BC7ZD5F5SQ6VIMCOQ"}],"data":"Yg=="}`, string(b.block.Data))
	})
}

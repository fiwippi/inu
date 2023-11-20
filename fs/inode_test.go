package fs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestINode_Raw(t *testing.T) {
	r := raw("a")
	require.Equal(t, rawKind, r.kind())

	// Marshal
	data, err := r.MarshalJSON()
	require.NoError(t, err)
	require.Equal(t, `{"kind":0,"data":"YQ=="}`, string(data))

	// Unmarshal
	//
	// To check the data inside is correct
	// we re-marshal it and then check that
	// it equals to the original marshaling
	in, err := unmarshalInode(data)
	require.NoError(t, err)
	require.Equal(t, rawKind, in.kind())
	reData, err := in.MarshalJSON()
	require.NoError(t, err)
	require.Equal(t, data, reData)
}

func TestINode_Directory(t *testing.T) {
	d := directory{}
	require.Equal(t, directoryKind, d.kind())

	// Marshal
	data, err := d.MarshalJSON()
	require.NoError(t, err)
	require.Equal(t, `{"kind":1}`, string(data))

	// Unmarshal
	in, err := unmarshalInode(data)
	require.NoError(t, err)
	require.Equal(t, directoryKind, in.kind())
}

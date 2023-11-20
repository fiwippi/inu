package fs

import (
	"encoding/json"
	"fmt"
)

type kind uint

const (
	rawKind kind = iota
	directoryKind
)

type inode interface {
	json.Marshaler
	kind() kind
}

var (
	_ inode = raw{}
	_ inode = directory{}
)

type raw []byte

func (r raw) MarshalJSON() ([]byte, error) {
	type _raw raw
	return json.Marshal(struct {
		K kind `json:"kind"`
		D _raw `json:"data"`
	}{
		K: r.kind(),
		D: _raw(r),
	})
}

func (r raw) kind() kind { return rawKind }

type directory struct{}

func (d directory) MarshalJSON() ([]byte, error) {
	type _directory directory
	return json.Marshal(struct {
		K kind `json:"kind"`
	}{
		K: d.kind(),
	})
}

func (d directory) kind() kind { return directoryKind }

func unmarshalInode(data []byte) (inode, error) {
	// First verify we can decode the type
	iKind := struct {
		K kind `json:"kind"`
	}{}
	if err := json.Unmarshal(data, &iKind); err != nil {
		return nil, err
	}

	// Now unmarshal based on the type
	switch iKind.K {
	case rawKind:
		type _raw raw
		v := struct {
			K kind `json:"kind"`
			D _raw `json:"data"`
		}{}
		err := json.Unmarshal(data, &v)
		return raw(v.D), err
	case directoryKind:
		return directory{}, nil
	}

	// If we're here we have an invalid kind
	return nil, fmt.Errorf("invalid inode kind")
}

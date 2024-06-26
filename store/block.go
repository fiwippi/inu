package store

import (
	"fmt"

	"inu/cid"
)

const MaxBlockSize = 5 * 1024 * 1024

type Block struct {
	CID  cid.CID `json:"cid"`
	Data []byte  `json:"data"`
}

func NewBlock(data []byte) (Block, error) {
	if len(data) > MaxBlockSize {
		return Block{}, fmt.Errorf("data exceeds max size")
	}

	return Block{
		CID:  cid.New(data),
		Data: data,
	}, nil
}

func (b *Block) Valid() bool {
	return cid.New(b.Data) == b.CID
}

func (b *Block) Copy() Block {
	tmp := make([]byte, len(b.Data))
	copy(tmp, b.Data)
	return Block{
		CID:  b.CID,
		Data: tmp,
	}
}

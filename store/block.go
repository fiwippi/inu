package store

import (
	"fmt"

	"inu"
)

const MaxBlockSize = 5 * 1024 * 1024

type Block struct {
	CID  inu.CID `json:"cid"`
	Data []byte  `json:"data"`
}

func NewBlock(data []byte) (Block, error) {
	if len(data) > MaxBlockSize {
		return Block{}, fmt.Errorf("data exceeds max size")
	}

	return Block{
		CID:  inu.NewCID(data),
		Data: data,
	}, nil
}

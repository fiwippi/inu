package inu

import (
	"crypto/sha256"
	"encoding/base32"
)

type CID string

func NewCID(data []byte) CID {
	// Digest the data
	h := sha256.New()
	h.Write(data)
	digest := h.Sum(nil)

	// Assertion to validate state of program
	if len(digest) != 32 {
		panic("invalid CID length")
	}

	// Encode it to base32
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest)
	return CID(b32)
}

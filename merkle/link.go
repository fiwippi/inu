package merkle

import (
	"inu/cid"
)

type Link struct {
	// Optional name
	Name string `json:"name"`

	// Cryptographic hash of the target node
	CID cid.CID `json:"cid"`
}

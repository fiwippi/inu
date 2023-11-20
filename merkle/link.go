package merkle

import "inu"

type Link struct {
	// Name of the link -- utf-8
	//
	// If the source node of the link is a directory,
	// the link name exists and represents the names
	// of the file that it points to
	Name string `json:"name"`

	// Cryptographic hash of the target node
	CID inu.CID `json:"cid"`
}

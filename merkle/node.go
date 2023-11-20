package merkle

import (
	"encoding/json"

	"inu/store"
)

type attributes struct {
	// Links to child nodes in the DAG
	Links []Link `json:"links"`

	// Data of the current node
	Data []byte `json:"data"`
}

type Node struct {
	// Store the node's links and data
	attr attributes

	// Stores the block representation of the node
	//
	// This guarantees the node can be successfully
	// encoded with a valid CID
	block store.Block
}

type NodeBuilder struct {
	attr attributes
}

func NewNodeBuilder() *NodeBuilder {
	return &NodeBuilder{attr: attributes{
		Links: make([]Link, 0),
	}}
}

func (b *NodeBuilder) SetData(data []byte) *NodeBuilder {
	b.attr.Data = data

	return b
}

func (b *NodeBuilder) AddLink(name string, n *Node) *NodeBuilder {
	b.attr.Links = append(b.attr.Links, Link{
		Name: name,
		CID:  n.block.CID,
	})

	return b
}

func (b *NodeBuilder) Build() (Node, error) {
	// Encode the node data into JSON and store
	// it in a block
	enc, err := json.Marshal(b.attr)
	if err != nil {
		return Node{}, err
	}
	block, err := store.NewBlock(enc)
	if err != nil {
		return Node{}, err
	}

	// Store the node's attributes and its
	// block representation in the node struct
	return Node{
		attr:  b.attr,
		block: block,
	}, nil
}

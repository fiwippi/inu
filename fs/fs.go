package fs

import (
	"fmt"
	"os"
	"strings"

	"inu"
	"inu/merkle"
	"inu/store"
)

const NumChildren = 2

const ChunkSize = 256 * 1024

type Record struct {
	CID  inu.CID
	Path string
}

type FS struct {
	store *store.Store
}

func NewFS(path string) *FS {
	return &FS{store: store.NewStore(path)}
}

func (fs *FS) AddPath(path string) (merkle.Node, []Record, error) {
	info, err := os.Stat(path)
	if err != nil {
		return merkle.Node{}, nil, err
	}

	// If we point to a file then create a DAG
	// and return it
	if info.Mode().IsRegular() {
		data, err := os.ReadFile(path)
		if err != nil {
			return merkle.Node{}, nil, err
		}

		n, err := fs.AddBytes(data)
		if err != nil {
			return merkle.Node{}, nil, err
		}

		return n, []Record{{n.Block().CID, path}}, nil
	}

	// Otherwise we build the dir node and keep track
	// of the added files and directories
	records := make([]Record, 0)
	builder := merkle.NewNodeBuilder()

	entries, err := os.ReadDir(path)
	if err != nil {
		return merkle.Node{}, nil, err
	}
	for _, e := range entries {
		// Read each item in the directory
		child, recs, err := fs.AddPath(path + "/" + e.Name())
		if err != nil {
			return merkle.Node{}, nil, err
		}

		// Keep track of the records
		records = append(records, recs...)

		// Link the child to its parent
		builder.AddLink(e.Name(), &child)
	}

	// Now we store the directory node and
	// continue propagating the records
	n, err := builder.Build()
	if err != nil {
		return merkle.Node{}, nil, err
	}
	if err := fs.store.Put(n.Block()); err != nil {
		return merkle.Node{}, nil, err
	}

	r := Record{n.Block().CID, path}
	return n, append(records, r), nil
}

func (fs *FS) AddBytes(bytes []byte) (merkle.Node, error) {
	nodes := make([]merkle.Node, 0)
	builder := merkle.NewNodeBuilder()

	// Wrap each chunk in an INode and
	// put it in the block store
	for _, c := range chunks(bytes, ChunkSize) {
		builder.Reset()

		// Create the raw inode and encode it
		enc, err := raw(c).MarshalJSON()
		if err != nil {
			return merkle.Node{}, err
		}

		// Store the encoded data in a merkle node
		n, err := builder.SetData(enc).Build()
		if err != nil {
			return merkle.Node{}, err
		}

		// Store the merkle node in the block store
		if err := fs.store.Put(n.Block()); err != nil {
			return merkle.Node{}, err
		}

		// Keep track of the node
		nodes = append(nodes, n)
	}

	// Build each level of the tree, if we
	// only have one merkle node then we
	// can return immediately though
	if len(nodes) == 1 {
		return nodes[0], nil
	}
	return fs.buildDAGLevel(chunks(nodes, NumChildren))

}

func (fs *FS) buildDAGLevel(children [][]merkle.Node) (merkle.Node, error) {
	parents := make([]merkle.Node, 0)
	builder := merkle.NewNodeBuilder()

	for _, cs := range children {
		builder.Reset()

		// Link the children to the parent node.
		// The parent node takes care of hashing
		// each child
		for _, c := range cs {
			builder.AddLink("", &c)
		}

		// Build the parent node and
		// put it in the store
		p, err := builder.Build()
		if err != nil {
			return merkle.Node{}, err
		}
		if err := fs.store.Put(p.Block()); err != nil {
			return merkle.Node{}, err
		}

		// Keep track of the parent
		parents = append(parents, p)
	}

	// If we only have one parent then we are
	// at the root and can exit, otherwise we
	// continue building the next level of the
	// tree
	if len(parents) == 1 {
		return parents[0], nil
	}
	return fs.buildDAGLevel(chunks(parents, NumChildren))
}

func (fs *FS) ReadBytes(n *merkle.Node) ([]byte, error) {
	// Return data if at leaf
	if len(n.Links()) == 0 {
		// First decode the inode
		in, err := unmarshalInode(n.Data())
		if err != nil {
			return nil, err
		}

		// Verify the inode is a raw
		// chunk and return its data
		switch in.kind() {
		case rawKind:
			return in.(raw), nil
		case directoryKind:
			return nil, fmt.Errorf("cannot read directory inode")
		}
	}

	// Otherwise append data from leaves
	data := make([]byte, 0)
	for _, l := range n.Links() {
		// Retrieve the node the leaf points
		// to from the block store
		b, err := fs.store.Get(l.CID)
		if err != nil {
			return nil, err
		}
		m, err := merkle.ParseBlock(b)
		if err != nil {
			return nil, err
		}

		// Read this nodes chunks
		chunk, err := fs.ReadBytes(&m)
		if err != nil {
			return nil, err
		}

		// Append this to the total data
		data = append(data, chunk...)
	}

	return data, nil
}

func (fs *FS) ResolvePath(path string) (merkle.Node, error) {
	// Split the path into its parts
	parts := strings.Split(path, string(os.PathSeparator))
	if len(parts) == 0 {
		return merkle.Node{}, fmt.Errorf("invalid path")
	}

	// Begin resolving the path
	return fs.resolvePath(inu.CID(parts[0]), parts[1:])
}

func (fs *FS) resolvePath(cid inu.CID, parts []string) (merkle.Node, error) {
	// Get the node for the CID
	b, err := fs.store.Get(cid)
	if err != nil {
		return merkle.Node{}, err
	}
	n, err := merkle.ParseBlock(b)
	if err != nil {
		return merkle.Node{}, err
	}

	// If we have no more entries to
	// traverse we can return the node
	if len(parts) == 0 {
		return n, nil
	}

	// Otherwise find the child link with
	// the name corresponding to the head
	// of the path
	name := parts[0]
	for _, l := range n.Links() {
		if name == l.Name {
			return fs.resolvePath(l.CID, parts[1:])
		}
	}

	// If we're here we failed to find a link
	return merkle.Node{}, fmt.Errorf("invalid path")
}

func chunks[T any](xs []T, size int) (chunks [][]T) {
	for size < len(xs) {
		xs, chunks = xs[size:], append(chunks, xs[0:size:size])
	}
	return append(chunks, xs)
}

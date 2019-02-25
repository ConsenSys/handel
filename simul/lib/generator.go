package lib

import (
	"crypto/rand"

	h "github.com/ConsenSys/handel"
)

// GenerateNodes create the necessary key pair & identites out of the given addresses.
// The IDs will be created sequentially from 0.
func GenerateNodes(cons Constructor, addresses []string) []*Node {
	nodes := make([]*Node, len(addresses))
	for i, addr := range addresses {
		nodes[i] = GenerateNode(cons, i, addr, true)
	}
	return nodes
}

// GenerateNode create the necessary key pair & identites out of the given addresses.
// for a singel node
func GenerateNode(cons Constructor, idx int, addr string, active bool) *Node {
	sec, pub := cons.KeyPair(rand.Reader)
	id := h.NewStaticIdentity(int32(idx), addr, pub)
	return &Node{SecretKey: sec, Identity: id, Active: active}
}

// GenerateNodesFromAllocation returns a list of Node from the allocation
// returned by Allocator + filled with the addresses
func GenerateNodesFromAllocation(cons Constructor, alloc map[string][]*NodeInfo) []*Node {
	var nodes []*Node
	for _, list := range alloc {
		for _, ni := range list {
			nodes = append(nodes, GenerateNode(cons, ni.ID, ni.Address, ni.Active))
		}
	}
	return nodes
}

// WriteAll writes down all the given nodes to the specified URI with the given
// parser.
func WriteAll(nodes []*Node, p NodeParser, uri string) {
	records := make([]*NodeRecord, len(nodes))
	for i, n := range nodes {
		rec, err := n.ToRecord()
		if err != nil {
			panic(err)
		}
		records[i] = rec
	}
	if err := p.Write(uri, records); err != nil {
		panic(err)
	}
}

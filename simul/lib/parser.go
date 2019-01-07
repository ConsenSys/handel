package lib

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"

	h "github.com/ConsenSys/handel"
)

// NodeParser is an interface that can read / write node records.
type NodeParser interface {
	// Reads all NodeRecords  from a given URI. It can be a CSV file
	// encoded for example.
	Read(uri string) ([]*NodeRecord, error)
	// Writes all node records to an URI. It can be a file.
	Write(uri string, records []*NodeRecord) error
}

// NodeList is a type that contains all informations on all nodes, and that
// implements the Registry interface. It is useful for binaries that retrieves
// multiple node information - not only the Identity.
type NodeList []*Node

// Node returns the Node structure at the given index
func (n *NodeList) Node(i int) *Node {
	if i < 0 || i > n.Size() {
		panic("that should not happen")
	}
	return (*n)[i]
}

// Size implements the Registry interface
func (n *NodeList) Size() int {
	return len(*n)
}

// Identity implements the Registry interface
func (n *NodeList) Identity(i int) (h.Identity, bool) {
	if i < 0 || i >= n.Size() {
		return nil, false
	}
	return (*n)[i].Identity, true
}

// Identities implements the Registry interface
func (n *NodeList) Identities(from, to int) ([]h.Identity, bool) {
	if from < 0 || from >= n.Size() || to < 0 || to > n.Size() {
		fmt.Println("requesting from", from, "to", to, " of nodelist", len(*n))
		return nil, false
	}
	sli := (*n)[from:to]
	ids := make([]h.Identity, len(sli))
	for i, n := range sli {
		ids[i] = n.Identity
	}
	return ids, true
}

// ReadAll reads the whole set of nodes from the given parser to the given URI.
// It returns the node list which can be used as a Registry as well
func ReadAll(uri string, parser NodeParser, c Constructor) (NodeList, error) {
	records, err := parser.Read(uri)
	if err != nil {
		return nil, err
	}
	var nodes NodeList
	for _, rec := range records {
		node, err := rec.ToNode(c)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

type csvParser struct{}

// NewCSVParser is a NodeParser that reads/writes to a CSV file
func NewCSVParser() NodeParser {
	return &csvParser{}
}

// Read implements NodeParser
func (c *csvParser) Read(uri string) ([]*NodeRecord, error) {
	file, err := os.Open(uri)
	defer file.Close()
	if err != nil {
		panic(err)
	}

	reader := bufio.NewReader(file)
	csvReader := csv.NewReader(reader)
	csvReader.FieldsPerRecord = 4
	var nodes []*NodeRecord
	for {
		line, err := csvReader.Read()
		if err != nil {
			if err == io.EOF {
				return nodes, nil
			}
			return nil, err
		}

		i, err := strconv.ParseInt(line[0], 10, 32)
		if err != nil {
			return nil, err
		}
		id := int32(i)
		addr := line[1]
		priv := line[2]
		pub := line[3]
		nodeRecord := &NodeRecord{ID: id, Addr: addr, Private: priv, Public: pub}
		nodes = append(nodes, nodeRecord)
	}
}

func (c *csvParser) Write(uri string, records []*NodeRecord) error {
	file, err := os.Create(uri)
	if err != nil {
		return err
	}
	defer file.Close()
	w := csv.NewWriter(file)
	for _, record := range records {
		line := []string{strconv.Itoa(int(record.ID)),
			record.Addr,
			record.Private,
			record.Public}
		if err := w.Write(line); err != nil {
			return err
		}
	}
	w.Flush()
	return nil
}

package p2p

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"github.com/ConsenSys/handel"
)

// Connector holds the logic to connect a node to a set of IDs on the overlay
// network
type Connector interface {
	Connect(node Node, ids handel.Registry, max int) error
}

type neighbor struct{}

// NewNeighborConnector returns a connector that connects to its most immediate
// neighbors - ids.
func NewNeighborConnector() Connector {
	return &neighbor{}
}

func (*neighbor) Connect(node Node, reg handel.Registry, max int) error {

	nodeID := int(node.Identity().ID())
	baseID := nodeID
	n := reg.Size()
	firstLoop := false
	for chosen := 0; chosen < max; chosen++ {
		if baseID == n {
			if firstLoop {
				fmt.Println("neighbor connection is looping!")
				panic("aie")
			}
			baseID = 0
			firstLoop = true
		}
		if baseID == nodeID {
			baseID++
			continue
		}
		id, ok := reg.Identity(baseID)
		if !ok {
			return errors.New("h-- identity not found")
		}
		if err := node.Connect(id); err != nil {
			return err
		}
		fmt.Printf("node %d connected to %d\n", nodeID, baseID)
		baseID++
	}
	return nil
}

type random struct{}

// NewRandomConnector returns a Connector that connects nodes randomly
func NewRandomConnector() Connector { return &random{} }

func (*random) Connect(node Node, reg handel.Registry, max int) error {
	n := reg.Size()
	own := node.Identity().ID()
	//fmt.Printf("- node %d connects to...", node.handelID)
	for chosen := 0; chosen < max; chosen++ {
		identity, ok := reg.Identity(rand.Intn(n))
		if !ok {
			return errors.New("invalid index")
		}
		if identity.ID() == own {
			chosen--
			continue
		}

		if err := node.Connect(identity); err != nil {
			return err
		}
		//fmt.Printf(" %d -", identity.Identity.ID())
	}
	//fmt.Printf("\n")
	return nil
}

func ExtractConnector(opts Opts) (Connector, int) {
	c, exists := opts.String("Connector")
	if !exists {
		c = "neighbor"
	}
	count, exists := opts.Int("Count")
	if !exists {
		count = MaxCount
	}
	var con Connector
	switch strings.ToLower(c) {
	case "neighbor":
		con = NewNeighborConnector()
		fmt.Println(" selecting NEIGHBOR connector with ", count)
	case "random":
		con = NewRandomConnector()
		fmt.Println(" selecting RANDOM connector with ", count)
	}
	return con, count

}

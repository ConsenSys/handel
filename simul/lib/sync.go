package lib

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ConsenSys/handel"
	"github.com/ConsenSys/handel/network"
	"github.com/ConsenSys/handel/network/udp"
)

// SyncMaster is a struct that handles the synchronization of all launched binaries
// by first expecting a message from each one of them, then sending them back a
// "START" message when all are ready. It uses UDP.
// The "Protocol" looks like this:
// - the SyncMaster listens on a UDP socket
// - each node sends a "READY" message to the starter over that socket.
// - the SyncMaster waits for n different READY messages.
// - once that is done, the SyncMaster sends a START message to all nodes.
//
// A READY message is a Packet which contains a structure inside the MultiSig
// field, as to re-use the UDP code already present.
type SyncMaster struct {
	sync.Mutex
	addr    string
	exp     int
	probExp int // probabilistically expected nb,i.e. 95% of exp
	total   int
	n       *udp.Network
	states  map[int]*state
}

type state struct {
	n         handel.Network
	id        int
	total     int
	probExp   int
	exp       int
	readys    map[int]bool
	addresses map[string]bool
	finished  chan bool
	done      bool
}

func newState(net handel.Network, id, total, exp, probExp int) *state {
	return &state{
		n:         net,
		id:        id,
		total:     total,
		exp:       exp,
		probExp:   probExp,
		readys:    make(map[int]bool),
		addresses: make(map[string]bool),
		finished:  make(chan bool, 1),
	}
}

func (s *state) WaitFinish() chan bool {
	return s.finished
}

func (s *state) newMessage(msg *syncMessage) {
	if msg.State != s.id {
		panic("this should not happen")
	}
	// list all IDs received
	for _, id := range msg.IDs {
		_, stored := s.readys[id]
		if !stored {
			// only store them once
			s.readys[id] = true
		}
	}
	// and store the address to send back the OK
	_, stored := s.addresses[msg.Address]
	if !stored {
		s.addresses[msg.Address] = true
	}
	fmt.Print(s.String())
	if len(s.readys) < s.exp {
		if len(s.readys) >= s.probExp {
			fmt.Printf("\n\n\n PROBABLILISTICALLY SYNCED AT 0.95\n\n\n")
		} else {
			return
		}
	}

	// send the messagesssss
	outgoing := &syncMessage{State: s.id}
	buff, err := outgoing.ToBytes()
	if err != nil {
		panic(err)
	}
	packet := &handel.Packet{MultiSig: buff}
	ids := make([]handel.Identity, 0, len(s.addresses))
	for address := range s.addresses {
		id := handel.NewStaticIdentity(0, address, nil)
		ids = append(ids, id)
	}
	go func() {
		if len(s.readys) >= s.exp && !s.done {
			s.finished <- true
			s.done = true
		}
		for i := 0; i < retrials; i++ {
			s.n.Send(ids, packet)
			time.Sleep(1 * time.Second)
		}
	}()

}

func (s *state) String() string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Sync Master ID %d received %d/%d status\n", s.id, len(s.readys), s.exp)
	for id := 0; id < s.total; id++ {
		_, ok := s.readys[id]
		if !ok {
			fmt.Fprintf(&b, "\t- %03d -absent-  ", id)
		} else {
			//for id, msg := range s.readys {
			//_, port, _ := net.SplitHostPort(msg.Address)
			fmt.Fprintf(&b, "\t- %03d +finished+", id)
		}
		if (id+1)%4 == 0 {
			fmt.Fprintf(&b, "\n")
		}
	}
	fmt.Fprintf(&b, "\n")
	return b.String()
}

// NewSyncMaster returns an SyncMaster that listens on the given address,
// for a expected number of READY messages.
func NewSyncMaster(addr string, expected, total int) *SyncMaster {
	n, err := udp.NewNetwork(addr, network.NewGOBEncoding())
	if err != nil {
		panic(err)
	}
	s := new(SyncMaster)
	n.RegisterListener(s)
	s.probExp = int(math.Ceil(float64(expected) * 0.995))
	s.states = make(map[int]*state)
	s.total = total
	s.exp = expected
	s.n = n
	return s
}

// WaitAll returns
func (s *SyncMaster) WaitAll(id int) chan bool {
	return s.getOrCreate(id).WaitFinish()
}

func (s *SyncMaster) getOrCreate(id int) *state {
	s.Lock()
	defer s.Unlock()
	state, exist := s.states[id]
	if !exist {
		state = newState(s.n, id, s.total, s.exp, s.probExp)
		s.states[id] = state
	}
	return state
}

// NewPacket implements the Listener interface
func (s *SyncMaster) NewPacket(p *handel.Packet) {
	msg := new(syncMessage)
	if err := msg.FromBytes(p.MultiSig); err != nil {
		panic(err)
	}
	s.getOrCreate(msg.State).newMessage(msg)
}

// Stop stops the network layer of the syncmaster
func (s *SyncMaster) Stop() {
	s.Lock()
	defer s.Unlock()
	s.n.Stop()
}

// SyncSlave sends its state to the master and waits for a START message
type SyncSlave struct {
	sync.Mutex
	own    string
	master string
	net    *udp.Network
	ids    []int
	states map[int]*slaveState
}

type slaveState struct {
	sync.Mutex
	n        handel.Network
	addr     string // our own address
	master   string // master's address
	id       int    // id of the state
	sent     bool
	finished chan bool
	done     bool
}

func newSlaveState(n handel.Network, master, addr string, id int) *slaveState {
	return &slaveState{
		n:        n,
		id:       id,
		master:   master,
		addr:     addr,
		finished: make(chan bool, 1),
	}
}

func (s *slaveState) WaitFinish() chan bool {
	return s.finished
}

func (s *slaveState) signal(ids []int) {
	for i := 0; i < retrials; i++ {
		msg := &syncMessage{State: s.id, IDs: ids, Address: s.addr}
		buff, err := msg.ToBytes()
		if err != nil {
			panic(err)
		}
		packet := &handel.Packet{MultiSig: buff}
		id := handel.NewStaticIdentity(0, s.master, nil)
		s.n.Send([]handel.Identity{id}, packet)
		time.Sleep(wait)
		if s.isDone() {
			return
		}
	}
}

func (s *slaveState) isDone() bool {
	s.Lock()
	defer s.Unlock()
	return s.done
}

func (s *slaveState) newMessage(msg *syncMessage) {
	if msg.State != s.id {
		panic("this is not normal")
	}

	s.Lock()
	defer s.Unlock()
	if s.done {
		return
	}
	s.done = true
	s.finished <- true
}

// NewSyncSlave returns a Sync to use as a node in the system to synchronize
// with the master
func NewSyncSlave(own, master string, ids []int) *SyncSlave {
	n, err := udp.NewNetwork(own, network.NewGOBEncoding())
	if err != nil {
		panic(err)
	}
	slave := new(SyncSlave)
	n.RegisterListener(slave)
	slave.ids = ids
	slave.net = n
	slave.own = own
	slave.master = master
	slave.states = make(map[int]*slaveState)
	return slave
}

const retrials = 5
const wait = 1 * time.Second

// WaitMaster first signals the master node for this ID and returns the channel
// that gets signaled when the master sends back a message with the same id.
func (s *SyncSlave) WaitMaster(id int) chan bool {
	state := s.getOrCreate(id)
	go state.signal(s.ids)
	return state.WaitFinish()
}

func (s *SyncSlave) getOrCreate(id int) *slaveState {
	s.Lock()
	defer s.Unlock()
	state, exists := s.states[id]
	if !exists {
		state = newSlaveState(s.net, s.master, s.own, id)
		s.states[id] = state
	}
	return state
}

// NewPacket implements the Listener interface
func (s *SyncSlave) NewPacket(p *handel.Packet) {
	msg := new(syncMessage)
	if err := msg.FromBytes(p.MultiSig); err != nil {
		panic(err)
	}
	s.getOrCreate(msg.State).newMessage(msg)
}

// Stop the network layer of the syncslave
func (s *SyncSlave) Stop() {
	s.net.Stop()
}

const (
	// START id
	START = iota
	// END id
	END
)

// syncMessage is what is sent between a SyncMaster and a SyncSlave
type syncMessage struct {
	State   int    // the id of the state
	Address string // address of the slave
	IDs     []int  // ID of the slave - useful for debugging
}

func (s *syncMessage) ToBytes() ([]byte, error) {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	err := enc.Encode(s)
	return b.Bytes(), err
}

func (s *syncMessage) FromBytes(buff []byte) error {
	var b = bytes.NewBuffer(buff)
	dec := gob.NewDecoder(b)
	return dec.Decode(s)
}

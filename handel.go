package handel

import (
	"errors"
	"fmt"
	"sync"
)

// Handel is the principal struct that performs the large scale multi-signature
// aggregation protocol. Handel is thread-safe.
type Handel struct {
	sync.Mutex
	// Config holding parameters to Handel
	c *Config
	// Network enabling external communication with other Handel nodes
	net Network
	// Registry holding access to all Handel node's identities
	reg Registry
	// constructor to unmarshal signatures + aggregate pub keys
	cons Constructor
	// public identity of this Handel node
	id Identity
	// Message that is being signed during the Handel protocol
	msg []byte
	// signature over the message
	sig Signature
	// partitions the set of nodes at different levels
	part partitioner
	// signature store with different merging/caching strategy
	store signatureStore
	// processing of signature - verification strategy
	proc signatureProcessing
	// all handlers registered that acts on a new signature
	handlers []handler
	// completed levels, i.e. full signatures at each of these levels
	completed []byte
	// highest level attained by this handel node so far
	currLevel byte
	// maximum  level attainable ever for this set of nodes
	maxLevel byte
	// best final signature,i.e. at the last level, seen so far
	best *MultiSignature
	// channel to exposes multi-signatures to the user
	out chan MultiSignature
	// indicating whether handel is finished or not
	done bool
	// constant threshold of contributions required in a ms to be considered
	// valid
	threshold int
}

// NewHandel returns a Handle interface that uses the given network and
// registry. The identity is the public identity of this Handel's node. The
// constructor defines over which curves / signature scheme Handel runs. The
// message is the message to "multi-sign" by Handel.  The first config in the
// slice is taken if not nil. Otherwise, the default config generated by
// DefaultConfig() is used.
func NewHandel(n Network, r Registry, id Identity, c Constructor,
	msg []byte, s Signature, conf ...*Config) *Handel {
	h := &Handel{
		net:      n,
		reg:      r,
		part:     newBinTreePartition(id.ID(), r),
		id:       id,
		cons:     c,
		msg:      msg,
		sig:      s,
		maxLevel: byte(log2(r.Size())),
		out:      make(chan MultiSignature, 100),
	}
	h.proc = newFifoProcessing(h.store, h.part, c, msg)
	h.handlers = []handler{
		h.checkCompletedLevels,
		h.checkFinalSignature,
	}

	if len(conf) > 0 && conf[0] != nil {
		h.c = mergeWithDefault(conf[0], r.Size())
	} else {
		h.c = DefaultConfig(r.Size())
	}

	h.threshold = h.c.ContributionsThreshold(h.reg.Size())
	h.store = newReplaceStore(h.part, h.c.NewBitSet)
	h.store.Store(1, &MultiSignature{BitSet: h.c.NewBitSet(1), Signature: s})
	return h
}

// NewPacket implements the Listener interface for the network.
// it parses the packet and sends it to processing if the packet is properly
// formatted.
func (h *Handel) NewPacket(p *Packet) {
	h.Lock()
	defer h.Unlock()

	ms, err := h.parsePacket(p)
	if err != nil {
		logf(err.Error())
	}

	// sends it to processing
	h.proc.Incoming() <- sigPair{level: p.Level, ms: ms}
}

// Start the Handel protocol by sending signatures to peers in the first level,
// and by starting relevant sub routines.
func (h *Handel) Start() {
	h.Lock()
	defer h.Unlock()
	go h.proc.Start()
	go h.rangeOnVerified()
	h.startNextLevel()
}

// Stop the Handel protocol and all sub routines
func (h *Handel) Stop() {
	h.Lock()
	defer h.Unlock()
	h.proc.Stop()
	h.done = true
	close(h.out)
}

// FinalSignatures returns the channel over which final multi-signatures
// are sent over. These multi-signatures contain at least a threshold of
// contributions, as defined in the config.
func (h *Handel) FinalSignatures() chan MultiSignature {
	return h.out
}

// parsePacket returns the multisignature parsed from the given packet, or an
// error if the packet can't be unmarshalled, or contains erroneous data such as
// out of range level.  This method is NOT thread-safe and only meant for
// internal use.
func (h *Handel) parsePacket(p *Packet) (*MultiSignature, error) {
	if p.Origin >= int32(h.reg.Size()) {
		return nil, errors.New("handel: packet's origin out of range")
	}

	if int(p.Level) > log2(h.reg.Size()) {
		return nil, errors.New("handel: packet's level out of range")
	}

	ms := new(MultiSignature)
	err := ms.Unmarshal(p.MultiSig, h.cons.Signature(), h.c.NewBitSet)
	return ms, err
}

// rangeOnVerified continuously listens on the output channel of the signature
// processing routine for verified signatures. Each verified signatures is
// passed down to all registered handlers.
func (h *Handel) rangeOnVerified() {
	for v := range h.proc.Verified() {
		h.Lock()
		for _, handler := range h.handlers {
			handler(&v)
		}
		h.Unlock()
	}
}

// startNextLevel increase the currLevel counter and looks if there is a
// multisignature with the required threshold. It looks starting from the
// previous level down to level 1 and stops at the first one found. It sends
// this multi-signature to peers in the new level.
func (h *Handel) startNextLevel() {
	h.currLevel++
	if h.currLevel >= h.maxLevel {
		// protocol is finished
		logf("handel: protocol finished at level %d", h.currLevel)
		return
	}
	var ms *MultiSignature
	var ok bool
	for lvl := h.currLevel - 1; lvl >= 0; lvl-- {
		ms, ok = h.store.Best(lvl)
		if !ok {
			continue
		}
	}
	if ms == nil {
		logf("handel: no signature to send ...?")
		return
	}
	nodes, ok := h.part.PickNextAt(int(h.currLevel), h.c.CandidateCount)
	if !ok {
		// XXX This should not happen, but what if ?
		return
	}
	h.sendTo(h.currLevel, ms, nodes)
}

// handler is a function that takes a new verified signature and acts on it
// according to its own rule. It can be checking if it passes to a next level,
// checking if the protocol is finished, checking if a signature completes
// higher levels, etc. The store is guaranteed to have a multisignature present
// at the level indicated in the verifiedSig. Each handler is called in a thread
// safe manner, global lock is held during the call to handlers.
type handler func(s *sigPair)

// checkFinalSignature STORES the newly verified signature and then checks if a
// new better final signature, i.e. a signature at the last level, has been
// generated. If so, it sends it to the output channel.
func (h *Handel) checkFinalSignature(s *sigPair) {
	h.store.Store(s.level, s.ms)

	sigpair := h.store.BestCombined()
	if sigpair.level != h.maxLevel {
		return
	}

	if sigpair.ms.BitSet.Cardinality() < h.threshold {
		fmt.Println("throwing ouuutt", sigpair.ms.BitSet.Cardinality(), "instead of ", h.threshold)
		return
	}

	newBest := func(ms *MultiSignature) {
		if h.done {
			return
		}
		h.best = ms
		h.out <- *h.best
	}

	if h.best == nil {
		newBest(sigpair.ms)
		return
	}

	new := sigpair.ms.Cardinality()
	local := h.best.Cardinality()
	if new > local {
		newBest(sigpair.ms)
	}
}

// checNewLevel looks if the signature completes levels by iterating over all
// levels and check if new levels have been completed. For each newly completed
// levels, it sends the full signature to peers in the respective level.
func (h *Handel) checkCompletedLevels(s *sigPair) {
	for lvl := byte(1); lvl < h.maxLevel; lvl++ {
		if h.isCompleted(lvl) {
			continue
		}

		ms, ok := h.store.Best(lvl)
		if !ok {
			panic("something's wrong with the store")
		}
		fullSize, err := h.part.Size(int(lvl))
		if err != nil {
			panic("level should be verified before")
		}
		if ms.Cardinality() != fullSize {
			continue
		}

		// completed level !
		// TODO: if no new nodes are available, maybe send to same nodes again
		// in case for full signatures ?
		newNodes, ok := h.part.PickNextAt(int(lvl), h.c.CandidateCount)
		if !ok {
			logf("handel: no new nodes for completed level %d", lvl)
			continue
		}

		h.sendTo(lvl, ms, newNodes)
	}
}

func (h *Handel) sendTo(lvl byte, ms *MultiSignature, ids []Identity) {
	buff, err := ms.MarshalBinary()
	if err != nil {
		logf("handel: error marshalling multi-signature: %s", err)
		return
	}

	packet := &Packet{
		Origin:   h.id.ID(),
		Level:    lvl,
		MultiSig: buff,
	}
	h.net.Send(ids, packet)
}

// isCompleted returns true if the given level has already been completed, i.e.
// is in the list of completed levels.
func (h *Handel) isCompleted(level byte) bool {
	for _, l := range h.completed {
		if l == level {
			return true
		}
	}
	return false
}

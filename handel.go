package handel

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

type Level struct {
	id int
	nodes []Identity
	started bool
	completed bool
	finished bool
	pos int
	sent int
	currentBestSize int
}

func NewLevel(id int, nodes []Identity) *Level {
	if id <= 0 {
		panic("bad value for level id")
	}
	l := &Level{
		id,
		nodes,
		id == 1,
		id == 1, // For the first level, we need only our own sig
		false,
		0,
		0,
		0,
	}
	return l
}

func createLevels(r Registry, partitioner Partitioner) []Level{
	lvls := make( []Level, log2(r.Size()))

	for i := 0; i< len(lvls); i += 1 {
		nodes, _ := partitioner.PickNextAt(i+1, r.Size() + 1)
		lvls[i] = *NewLevel(i+1, nodes)
	}

	return lvls
}


func (c *Level) PickNextAt(count int) ([]Identity, bool) {
	size := min(count, len(c.nodes))
	res := make( []Identity, size)

	for i:=0; i<size; i++{
		res[i] = c.nodes[c.pos]
		c.pos++
		if c.pos >= len(c.nodes){
			c.pos = 0
		}
	}

	c.sent += size
	if c.sent >= len(c.nodes) {
		c.finished = true
	}

	return res, true
}

// check if the signature is better than what we have.
// If it's better, reset the counters of the messages sent.
// If the level is now completed we return true; if not we return false
func (l *Level) updateBestSig(sig *MultiSignature) (bool) {
	if l.completed || l.currentBestSize >= sig.BitSet.Cardinality() {
		return false
	}

	l.currentBestSize = sig.Cardinality()
	l.finished = false
	l.sent = 0

	// We consider that the best signature for a level could be a complete signature
	//  from a upper level, so we check for '>=' rather than '=='
	if l.currentBestSize >= len(l.nodes) {
		// If we completed the level we start it rather than waiting for
		//  a timeout condition
		l.started = true
	}

	return l.currentBestSize >= len(l.nodes)
}

// Send our best signature for this level, to 'count' nodes
// We expect the store to give us as the combined signature:
// Either a subset of the signature we need for this level
// Either the complete set of signature for our level
// Either a complete set of signatures from an upper level
func (h *Handel) sendUpdate(l Level, count int) {
	if !l.started || l.finished {
		return
	}

	sp := h.store.Combined(byte(l.id) - 1)
	if sp == nil {
		panic("THIS SHOULD NOT HAPPEN AT ALL")
	}
	newNodes, _ := l.PickNextAt(count)
	h.logf("sending out signature of lvl %d (size %d) to %v", l.id, sp.BitSet.BitLength(), newNodes)
	h.sendTo(l.id, sp, newNodes)
}

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
	// signature store with different merging/caching strategy
	store signatureStore
	// processing of signature - verification strategy
	proc signatureProcessing
	// all actors registered that acts on a new signature
	actors []actor
	// best final signature,i.e. at the last level, seen so far
	best *MultiSignature
	// channel to exposes multi-signatures to the user
	out chan MultiSignature
	// indicating whether handel is finished or not
	done bool
	// constant threshold of contributions required in a ms to be considered
	// valid
	threshold int
	// ticker for the periodic update
	ticker *time.Ticker
	// all the levels
	levels []Level
	// Start time of Handel
	startTime time.Time
}


// NewHandel returns a Handle interface that uses the given network and
// registry. The identity is the public identity of this Handel's node. The
// constructor defines over which curves / signature scheme Handel runs. The
// message is the message to "multi-sign" by Handel.  The first config in the
// slice is taken if not nil. Otherwise, the default config generated by
// DefaultConfig() is used.
func NewHandel(n Network, r Registry, id Identity, c Constructor,
	msg []byte, s Signature, conf ...*Config) *Handel {

	var config *Config
	if len(conf) > 0 && conf[0] != nil {
		config = mergeWithDefault(conf[0], r.Size())
	} else {
		config = DefaultConfig(r.Size())
	}

	part := config.NewPartitioner(id.ID(), r)
	firstBs := config.NewBitSet(1)
	firstBs.Set(0, true)
	mySig := &MultiSignature{BitSet: firstBs, Signature: s}

	h := &Handel{
		c:        config,
		net:      n,
		reg:      r,
		id:       id,
		cons:     c,
		msg:      msg,
		sig:      s,
		out:      make(chan MultiSignature, 1000),
		ticker:	  time.NewTicker(config.UpdatePeriod),
		levels:   createLevels(r, part),
	}
	h.actors = []actor{
		actorFunc(h.checkCompletedLevel),
		actorFunc(h.checkFinalSignature),
	}

	go func() {
		for t := range h.ticker.C {
			h.Lock()
			h.periodicUpdate(t)
			h.Unlock()
		}
	}()

	h.threshold = h.c.ContributionsThreshold(h.reg.Size())
	h.store = newReplaceStore(part, h.c.NewBitSet)
	h.store.Store(0, mySig)
	h.proc = newFifoProcessing(h.store, part, c, msg)
	h.net.RegisterListener(h)
	return h
}

// NewPacket implements the Listener interface for the network.
// it parses the packet and sends it to processing if the packet is properly
// formatted.
func (h *Handel) NewPacket(p *Packet) {
	h.Lock()
	defer h.Unlock()
	if h.done {
		return
	}
	ms, err := h.parsePacket(p)
	if err != nil {
		h.logf("invalid packet: %s", err)
		return
	}

	// sends it to processing
	h.logf("received packet from %d for level %d: %s", p.Origin, p.Level, ms.String())
	h.proc.Incoming() <- sigPair{origin: p.Origin, level: p.Level, ms: ms}
}

// Start the Handel protocol by sending signatures to peers in the first level,
// and by starting relevant sub routines.
func (h *Handel) Start() {
	h.Lock()
	defer h.Unlock()
	h.startTime = time.Now()
	go h.proc.Start()
	go h.rangeOnVerified()
	h.periodicUpdate(h.startTime)
}

// Stop the Handel protocol and all sub routines
func (h *Handel) Stop() {
	h.Lock()
	defer h.Unlock()
	h.ticker.Stop()
	h.proc.Stop()
	h.done = true
	close(h.out)
}

func (h *Handel) periodicUpdate(t time.Time) {
	msSinceStart := int(t.Sub(h.startTime).Seconds() * 1000)

	for _, lvl := range h.levels {
		// Check if the level is in timeout, and update it if necessary
		if !lvl.started && msSinceStart >= lvl.id * int(h.c.LevelTimeout.Seconds() * 1000){
			lvl.started = true
		}
		h.sendUpdate(lvl, 1)
	}
}

// FinalSignatures returns the channel over which final multi-signatures
// are sent over. These multi-signatures contain at least a threshold of
// contributions, as defined in the config.
func (h *Handel) FinalSignatures() chan MultiSignature {
	return h.out
}

// rangeOnVerified continuously listens on the output channel of the signature
// processing routine for verified signatures. Each verified signatures is
// passed down to all registered actors. Each handler is called in a thread safe
// manner, global lock is held during the call to actors.
func (h *Handel) rangeOnVerified() {
	for v := range h.proc.Verified() {
		h.logf("new verified signature received -> %s", v.String())
		h.store.Store(v.level, v.ms)
		h.Lock()
		for _, actor := range h.actors {
			actor.OnVerifiedSignature(&v)
		}
		h.Unlock()
	}
}

// actor is an interface that takes a new verified signature and acts on it
// according to its own rule. It can be checking if it passes to a next level,
// checking if the protocol is finished, checking if a signature completes
// higher levels so it should send it out to other peers, etc. The store is
// guaranteed to have a multisignature present at the level indicated in the
// verifiedSig. Each handler is called in a thread safe manner, global lock is
// held during the call to actors.
type actor interface {
	OnVerifiedSignature(s *sigPair)
}

type actorFunc func(s *sigPair)

func (a actorFunc) OnVerifiedSignature(s *sigPair) {
	a(s)
}

// checkFinalSignature STORES the newly verified signature and then checks if a
// new better final signature, i.e. a signature at the last level, has been
// generated. If so, it sends it to the output channel.
func (h *Handel) checkFinalSignature(s *sigPair) {
	sig := h.store.FullSignature()

	if sig.BitSet.Cardinality() < h.threshold {
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
		newBest(sig)
		return
	}

	newCard := sig.Cardinality()
	local := h.best.Cardinality()
	if newCard > local {
		newBest(sig)
	}
}

// When we have a new signature, multiple levels may be impacted. The store
//  is in charge of selecting the best signature for a level, so we will
//  call it for all levels.
// As well, if a level is completed, all the previous levels
//  are completed as well. For these reasons, we always check
//  all the levels, starting by the last one, and we:
//  1) Update the signature
//  2) If the level is now completed, we do a massive update
// Once we find a level that was already completed we stop.
func (h *Handel) checkCompletedLevel(s *sigPair) {
	for i := len(h.levels) - 1; i > 0; i-- {
		lvl := h.levels[i]
		if lvl.completed {
			return
		}
		ms, ok := h.store.Best(byte(lvl.id))
		if !ok {
			continue
		}
		if lvl.updateBestSig(ms) {
			h.sendUpdate(lvl, h.c.CandidateCount)
		}
	}
}

func (h *Handel) sendTo(lvl int, ms *MultiSignature, ids []Identity) {
	buff, err := ms.MarshalBinary()
	if err != nil {
		h.logf("error marshalling multi-signature: %s", err)
		return
	}

	packet := &Packet{
		Origin:   h.id.ID(),
		Level:    byte(lvl),
		MultiSig: buff,
	}
	h.net.Send(ids, packet)
}

// parsePacket returns the multisignature parsed from the given packet, or an
// error if the packet can't be unmarshalled, or contains erroneous data such as
// out of range level.  This method is NOT thread-safe and only meant for
// internal use.
func (h *Handel) parsePacket(p *Packet) (*MultiSignature, error) {
	if p.Origin >= int32(h.reg.Size()) {
		return nil, errors.New("packet's origin out of range")
	}

	lvl := int(p.Level)
	if lvl  < 1 || lvl > log2(h.reg.Size()) {
		msg := fmt.Sprintf("packet's level out of range, level received=%d, max=%d, nodes count=%d",
			lvl, log2(h.reg.Size()), h.reg.Size())
		return nil, errors.New(msg)
	}

	ms := new(MultiSignature)
	err := ms.Unmarshal(p.MultiSig, h.cons.Signature(), h.c.NewBitSet)
	return ms, err
}

func (h *Handel) logf(str string, args ...interface{}) {
	now := time.Now()
	timeSpent := fmt.Sprintf("%02d:%02d:%02d", now.Hour(),
		now.Minute(),
		now.Second())
	idArg := []interface{}{timeSpent, h.id.ID()}
	logf("%s: handel %d: "+str, append(idArg, args...)...)
}

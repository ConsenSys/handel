package handel

// this contains the logic for processing signatures asynchronously. Each
// incoming packet from the network is passed down to the signatureProcessing
// interface, and may be returned to main Handel logic when verified.

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var deathPillPair = sigPair{-1, 121, nil}

// signatureProcessing is an interface responsible for verifying incoming
// multi-signature. It can decides to drop some incoming signatures if deemed
// useless. It outputs verified signatures to the main handel processing logic
// It is an asynchronous processing interface that needs to be sendStarted and
// stopped when needed.
type signatureProcessing interface {
	// Start is a blocking call that starts the processing routine
	Start()
	// Stop is a blocking call that stops the processing routine
	Stop()
	// Add a sigpair to the processing list
	Add(sp *sigPair)
	// channel that outputs verified signatures. Implementation must guarantee
	// that all verified signatures are signatures that have been sent on the
	// incoming channel. No new signatures must be outputted on this channel (
	// is the role of the Store)
	Verified() chan sigPair
}

// SigEvaluator is an interface responsible to evaluate incoming *non-verified*
// signature according to their relevance regarding the running handel protocol.
// This is an important part of Handel because the aggregation function (pairing
// for bn256) can take some time, thus minimizing these number of operations is
// essential.
type SigEvaluator interface {
	// Evaluate the interest to verify a signature
	//   0: no interest, the signature can be discarded definitively
	//  >0: the greater the more interesting
	Evaluate(sp *sigPair) int
}

// Evaluator1 returns 1 for all signatures, leading to having all signatures
// verified.
type Evaluator1 struct {
}

// Evaluate implements the SigEvaluator interface.
func (f *Evaluator1) Evaluate(sp *sigPair) int {
	return 1
}

func newEvaluator1() SigEvaluator {
	return &Evaluator1{}
}

// EvaluatorStore is a wrapper around the store's evaluate strategy.
type EvaluatorStore struct {
	store signatureStore
}

// Evaluate implements the SigEvaluator strategy.
func (f *EvaluatorStore) Evaluate(sp *sigPair) int {
	return f.store.Evaluate(sp)
}

func newEvaluatorStore(store signatureStore) SigEvaluator {
	return &EvaluatorStore{store: store}
}

// evaluator processing processing incoming signatures according to an signature
// evalutor strategy.
type evaluatorProcessing struct {
	cond *sync.Cond

	h *Handel

	part Partitioner
	cons Constructor
	msg  []byte

	out       chan sigPair
	todos     []*sigPair
	evaluator SigEvaluator
	log       Logger

	sigSleepTime int64

	// Statistics on the activity
	// number of signatures checked by the processing
	sigCheckedCt int

	// Size of the queue after the cleanup (removal of the redundant signatures)
	sigQueueSize int

	// Number of signatures identified as redundant by the evaluation
	sigSuppressed int

	// Time spent checking the signature
	sigCheckingTime int
}

// TODO handel argument only for logging
func newEvaluatorProcessing(part Partitioner, c Constructor, msg []byte, sigSleepTime int, e SigEvaluator, log Logger) signatureProcessing {
	m := sync.Mutex{}

	ev := &evaluatorProcessing{
		cond: sync.NewCond(&m),
		part: part,
		cons: c,
		msg:  msg,
		sigSleepTime: int64(sigSleepTime),

		out:       make(chan sigPair, 1000),
		todos:     make([]*sigPair, 0),
		evaluator: e,
		log:       log,
	}
	return ev
}

func (f *evaluatorProcessing) Start() {
	go f.processLoop()
}

func (f *evaluatorProcessing) Stop() {
	f.Add(&deathPillPair)
}

func (f *evaluatorProcessing) Verified() chan sigPair {
	return f.out
}

func (f *evaluatorProcessing) Add(sp *sigPair) {
	f.cond.L.Lock()
	defer f.cond.L.Unlock()

	f.todos = append(f.todos, sp)
	f.cond.Signal()
}

// Look at the signatures received so far and select the one
//  that should be processed first.
func (f *evaluatorProcessing) readTodos() (bool, *sigPair) {
	f.cond.L.Lock()
	defer f.cond.L.Unlock()
	for len(f.todos) == 0 {
		f.cond.Wait()
	}

	previousLen := len(f.todos)

	// We need to iterate on our list. We put in
	//   'newTodos' the signatures not selected in this round
	//   but possibly interesting next time
	var newTodos []*sigPair
	var best *sigPair
	bestMark := 0
	for _, pair := range f.todos {
		if *pair == deathPillPair {
			return true, nil
		}
		if pair.ms == nil {
			continue
		}

		mark := f.evaluator.Evaluate(pair)
		if mark > 0 {
			if mark <= bestMark {
				newTodos = append(newTodos, pair)
			} else {
				if best != nil {
					newTodos = append(newTodos, best)
				}
				best = pair
				bestMark = mark
			}
		}
	}

	f.todos = newTodos

	newLen := len(f.todos)

	f.sigSuppressed +=  previousLen - newLen
	if best != nil {
		f.sigSuppressed-- // we don't want to count 'best' as a suppressed sig.
		f.sigCheckedCt++
		f.sigQueueSize += newLen
	}

	return false, best
}

func (f *evaluatorProcessing) hasTodos() bool {
	f.cond.L.Lock()
	defer f.cond.L.Unlock()
	return len(f.todos) > 0
}

func (f *evaluatorProcessing) processLoop() {
	sigCount := 0
	for {
		stop := f.processStep()
		if stop {
			return
		}
		sigCount++
		if sigCount%100 == 0 {
			f.log.Info("processed_sig", sigCount)
		}
	}
}

func (f *evaluatorProcessing) Values() map[string]float64 {
	sigQueueSize := 0.0
	sigCheckingTime := 0.0
	if f.sigCheckedCt > 0 {
		sigQueueSize = float64(f.sigQueueSize) / float64(f.sigCheckedCt)
		sigCheckingTime = float64(f.sigCheckingTime) / float64(f.sigCheckedCt)
	}

	return map[string]float64{
		"sigCheckedCt": float64(f.sigCheckedCt),
		"sigQueueSize": sigQueueSize,
		"sigSuppressed": float64(f.sigSuppressed),
		"sigCheckingTime": sigCheckingTime,
	}
}

func (f *evaluatorProcessing) processStep() bool {
	done, best := f.readTodos()
	if done {
		close(f.out)
		return true
	}
	if best != nil {
		f.verifyAndPublish(best)
	}
	return false
}

func (f *evaluatorProcessing) verifyAndPublish(sp *sigPair) {
	startTime := time.Now()
	err := (error)(nil)
	if f.sigSleepTime <= 0 {
		err = verifySignature(sp, f.msg, f.part, f.cons)
	} else {
		time.Sleep(time.Duration(f.sigSleepTime * 1000000))
		err = nil
	}
	endTime := time.Now()

	f.sigCheckingTime += int(endTime.Sub(startTime).Nanoseconds() / 1000000)

	if err != nil {
		f.log.Warn("verify", err)
	} else {
		f.out <- *sp
	}
}

// fifoProcessing implements the signatureProcessing interface using a simple
// fifo queue, verifying all incoming signatures, not matter relevant or not.
type fifoProcessing struct {
	sync.Mutex
	store signatureStore
	part  Partitioner
	cons  Constructor
	msg   []byte
	in    chan sigPair
	out   chan sigPair
	done  bool
}

// newFifoProcessing returns a signatureProcessing implementation using a fifo
// queue. It needs the store to store the valid signatures, the partitioner +
// constructor + msg to verify the signatures.
func newFifoProcessing(store signatureStore, part Partitioner,
	c Constructor, msg []byte) signatureProcessing {
	return &fifoProcessing{
		part:  part,
		store: store,
		cons:  c,
		msg:   msg,
		in:    make(chan sigPair, 100),
		out:   make(chan sigPair, 100),
	}
}

// processIncoming simply verifies the signature, stores it, and outputs it
func (f *fifoProcessing) processIncoming() {
	for pair := range f.in {
		score := f.store.Evaluate(&pair)
		if score == 0 {
			//logf("handel: fifo: skipping verification of signature %s", pair.String())
			continue
		}

		err := f.verifySignature(&pair)
		if err != nil {
			logf("handel: fifo: verifying err: %s", err)
			continue
		}

		f.Lock()
		done := f.done
		if !done {
			//logf("handel: handling back verified signature to actors")
			f.out <- pair
		}
		f.Unlock()
		if done {
			break
		}
	}
}


func (f *fifoProcessing) verifySignature(pair *sigPair) error {
	level := pair.level
	ms := pair.ms
	ids, err := f.part.IdentitiesAt(int(level))
	if err != nil {
		return err
	}

	if ms.BitSet.BitLength() != len(ids) {
		return errors.New("handel: inconsistent bitset with given level")
	}

	// compute the aggregate public key corresponding to bitset
	aggregateKey := f.cons.PublicKey()
	for i := 0; i < ms.BitSet.BitLength(); i++ {
		if !ms.BitSet.Get(i) {
			continue
		}
		aggregateKey = aggregateKey.Combine(ids[i].PublicKey())
	}

	if err := aggregateKey.VerifySignature(f.msg, ms.Signature); err != nil {
		logf("processing err: from %d -> level %d -> %s", pair.origin, pair.level, ms.String())
		return fmt.Errorf("handel: %s", err)
	}

	return nil
}

func (f *fifoProcessing) Add(sp *sigPair) {
	f.in <- *sp
}

func (f *fifoProcessing) Verified() chan sigPair {
	return f.out
}

func (f *fifoProcessing) Start() {
	f.processIncoming()
}

func (f *fifoProcessing) Stop() {
	f.Lock()
	defer f.Unlock()
	if f.done {
		return
	}
	f.done = true
	close(f.in)
	close(f.out)
}

func (f *fifoProcessing) isStopped() bool {
	f.Lock()
	defer f.Unlock()
	// OK since once we call stop, we'll no go back to done = false
	return f.done
}

func verifySignature(pair *sigPair, msg []byte, part Partitioner, cons Constructor) error {
	level := pair.level
	ms := pair.ms
	ids, err := part.IdentitiesAt(int(level))
	if err != nil {
		return err
	}

	if ms.BitSet.BitLength() != len(ids) {
		return errors.New("handel: inconsistent bitset with given level")
	}

	// compute the aggregate public key corresponding to bitset
	aggregateKey := cons.PublicKey()
	for i := 0; i < ms.BitSet.BitLength(); i++ {
		if !ms.BitSet.Get(i) {
			continue
		}
		aggregateKey = aggregateKey.Combine(ids[i].PublicKey())
	}

	if err := aggregateKey.VerifySignature(msg, ms.Signature); err != nil {
		logf("processing err: from %d -> level %d -> %s", pair.origin, pair.level, ms.String())
		return fmt.Errorf("handel: %s", err)
	}
	return nil
}

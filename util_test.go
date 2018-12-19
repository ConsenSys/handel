package handel

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

func FakeRegistry(size int) Registry {
	ids := make([]Identity, size)
	for i := 0; i < size; i++ {
		ids[i] = &fakeIdentity{int32(i), &fakePublic{true}}
	}
	return NewArrayRegistry(ids)
}

type fakePublic struct {
	verify bool
}

func (f *fakePublic) String() string {
	return fmt.Sprintf("public-%v", f.verify)
}
func (f *fakePublic) VerifySignature(b []byte, s Signature) error {
	if !f.verify {
		return errors.New("wrong")
	}
	if !s.(*fakeSig).verify {
		return errors.New("wrong")
	}
	return nil
}
func (f *fakePublic) Combine(p PublicKey) PublicKey {
	return &fakePublic{f.verify && p.(*fakePublic).verify}
}

type fakeIdentity struct {
	id int32
	*fakePublic
}

func (f *fakeIdentity) Address() string {
	return fmt.Sprintf("fake-%d-%v", f.id, f.fakePublic.verify)
}
func (f *fakeIdentity) PublicKey() PublicKey { return f.fakePublic }
func (f *fakeIdentity) ID() int32            { return f.id }
func (f *fakeIdentity) String() string       { return f.Address() }

type fakeSecret struct {
}

func (f *fakeSecret) PublicKey() PublicKey {
	return &fakePublic{true}
}

func (f *fakeSecret) Sign(msg []byte, rand io.Reader) (Signature, error) {
	return &fakeSig{true}, nil
}

var fakeConstSig = []byte{0x01, 0x02, 0x3, 0x04}

type fakeSig struct {
	verify bool
}

func (f *fakeSig) MarshalBinary() ([]byte, error) {
	var b bytes.Buffer
	var i byte
	if f.verify {
		i = 1
	}

	err := binary.Write(&b, binary.BigEndian, i)
	return b.Bytes(), err

}

func (f *fakeSig) UnmarshalBinary(buff []byte) error {
	var b = bytes.NewBuffer(buff)
	var i byte
	err := binary.Read(b, binary.BigEndian, &i)
	if err != nil {
		return err
	}
	if i == 1 {
		f.verify = true
	}
	return nil
}

func (f *fakeSig) Combine(Signature) Signature {
	return f
}

func (f *fakeSig) String() string {
	return fmt.Sprintf("fake{%v}", f.verify)
}

type fakeCons struct {
}

func (f *fakeCons) Signature() Signature {
	return new(fakeSig)
}

func (f *fakeCons) PublicKey() PublicKey {
	return &fakePublic{true}
}

func fullBitset(level int) BitSet {
	if level != 0 {
		level = level - 1
	}
	size := int(math.Pow(2, float64(level)))
	return finalBitset(size)
}

// returns a multisignature from a bitset
func newSig(b BitSet) *MultiSignature {
	return &MultiSignature{
		BitSet:    b,
		Signature: &fakeSig{true},
	}
}

func fullSig(level int) *MultiSignature {
	return newSig(fullBitset(level))
}

func fullSigPair(level int) *sigPair {
	return &sigPair{
		level: byte(level),
		ms:    fullSig(level),
	}
}

func finalBitset(size int) BitSet {
	bs := NewWilffBitset(size)
	for i := 0; i < size; i++ {
		bs.Set(i, true)
	}
	return bs
}

// returns a final signature pair associated with a given level but with a full
// size bitset ( n )
func finalSigPair(level, size int) *sigPair {
	return &sigPair{
		level: byte(level),
		ms:    newSig(finalBitset(size)),
	}
}

func mkSigPair(level int) *sigPair {
	return &sigPair{
		level: byte(level),
		ms:    fullSig(level),
	}
}

func sigPairs(lvls ...int) []*sigPair {
	s := make([]*sigPair, len(lvls))
	for i, lvl := range lvls {
		s[i] = mkSigPair(lvl)
	}
	return s
}

func sigs(sigs ...*sigPair) []*sigPair {
	return sigs
}

func FakeSetup(n int) (Registry, []*Handel) {
	reg := FakeRegistry(n).(*arrayRegistry)
	ids := reg.ids
	nets := make([]Network, n)
	for i := 0; i < reg.Size(); i++ {
		nets[i] = &TestNetwork{ids[i].ID(), nets, nil}
	}
	cons := new(fakeCons)
	handels := make([]*Handel, n)
	newPartitioner := func(id int32, reg Registry) Partitioner {
		return NewRandomBinPartitioner(id, reg, nil)
		//return NewBinPartitioner(id,reg)
	}
	conf := &Config{NewPartitioner: newPartitioner}
	for i := 0; i < n; i++ {
		handels[i] = NewHandel(nets[i], reg, ids[i], cons, msg, &fakeSig{true}, conf)
	}
	return reg, handels
}

type listenerFunc func(*Packet)

func (l listenerFunc) NewPacket(p *Packet) {
	l(p)
}

func ChanListener(ch chan *Packet) Listener {
	return listenerFunc(func(p *Packet) {
		ch <- p
	})
}

func CloseHandels(hs []*Handel) {
	for _, h := range hs {
		h.Stop()
	}
}

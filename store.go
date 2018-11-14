package handel

// signatureStore is a generic interface whose role is to store received valid
// multisignature, and to be able to serve the best multisignature received so
// far at a given level. Different strategies can be implemented such as keeping
// only the best one, merging two non-colluding multi-signatures etc
type signatureStore interface {
	// Store saves the multi-signature if it is "better"
	// (implementation-dependent) than the one previously saved at the same
	// level. It returns true if the entry for this level has been updated,i.e.
	// if GetBest at the same level will return a new multi-signature.
	Store(level byte, ms *MultiSignature) bool
	// GetBest returns the "best" multisignature at the requested level. Best
	// should be interpreted as "containing the most individual contributions".
	Best(level byte) (*MultiSignature, bool)
	// Highest returns the highest multisignature contains as well as the
	// corresponding level. It is nil if not such multisignatures have been
	// found.
	Highest() *sigPair
}

type sigPair struct {
	level byte
	ms    *MultiSignature
}

// replaceStore is a signatureStore that only stores multisignature if it
// contains more individual contributions than what's already stored.
type replaceStore struct {
	m       map[byte]*MultiSignature
	highest byte
}

func newReplaceStore() *replaceStore {
	return &replaceStore{
		m: make(map[byte]*MultiSignature),
	}
}

func (r *replaceStore) Store(level byte, ms *MultiSignature) bool {
	ms2, ok := r.m[level]
	if !ok {
		r.store(level, ms)
		return true
	}

	c1 := ms.Cardinality()
	c2 := ms2.Cardinality()
	if c1 > c2 {
		r.store(level, ms)
		return true
	}
	return false
}

func (r *replaceStore) Best(level byte) (*MultiSignature, bool) {
	ms, ok := r.m[level]
	return ms, ok
}

func (r *replaceStore) Highest() *sigPair {
	ms, ok := r.m[r.highest]
	if !ok {
		return nil
	}
	return &sigPair{
		level: r.highest,
		ms:    ms,
	}
}

func (r *replaceStore) store(level byte, ms *MultiSignature) {
	r.m[level] = ms
	if level > r.highest {
		r.highest = level
	}
}
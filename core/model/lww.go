package model

// LWW is a generic last-write-wins register ordered by Hybrid Logical Clock
// with the deterministic device-identifier tie break required by the sync
// protocol for note titles, tags, notebook assignment, and flags.
type LWW[T any] struct {
	Value T
	Clock HLC
}

// Merge returns the register that should win when two replicas hold
// independently produced values for the same logical field: the register
// with the later clock, per HLC.Compare. Equal clocks keep the receiver;
// that case is reachable only when both registers were produced by the same
// event, so the two values are already identical.
func (r LWW[T]) Merge(other LWW[T]) LWW[T] {
	if other.Clock.Compare(r.Clock) > 0 {
		return other
	}
	return r
}

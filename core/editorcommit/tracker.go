// Package editorcommit tracks generation-tagged local commit receipts for
// one editor session, per design.md decision 2 ("Model editor persistence
// as generation-tagged local commits"): input marks a session dirty and
// advances its generation, and only a commit completion for the latest
// generation may report a durable Saved state. A completion for a
// generation superseded by newer dirty input is stale and must never
// overwrite what newer input has already reported, whether that stale
// completion succeeded or failed.
package editorcommit

import (
	"sync"

	"github.com/beresta-app/beresta/core/presentation"
)

// Generation identifies one point of local input for one editor session,
// in strictly increasing order starting at 1. The zero Generation is
// never issued by Tracker.Dirty; it marks "no input has occurred yet".
type Generation uint64

// Tracker is the generation-tagged commit state machine for one open
// editor session (one note being edited). It performs no I/O of its own:
// the caller still commits through core/account.CommitNoteBody (desktop)
// or its core/mobileapi equivalent; Tracker only decides whether a
// commit's completion may still change the session's reported
// presentation.LocalSaveState. Tracker is safe for concurrent use.
type Tracker struct {
	mu     sync.Mutex
	latest Generation
	// saved is the highest generation Accept has confirmed durably saved,
	// meaningful only once hasSaved is true (the zero Generation is a
	// legitimate commit target - see Generation's doc comment - so it
	// cannot double as its own "nothing saved yet" sentinel). saved only
	// ever advances on a success, never on a failure: a retry of the same
	// (unchanged) generation - the common case, since a retry resends
	// identical content and so never calls Dirty again - must still be
	// able to move that generation from CouldNotSave to Saved.
	saved    Generation
	hasSaved bool
}

// Dirty marks the session dirty with new local input and returns the new
// current generation. The caller schedules a debounced commit that will
// eventually call Accept with this generation (or a later one, if more
// input arrives before that commit starts).
func (t *Tracker) Dirty() Generation {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.latest++
	return t.latest
}

// Latest returns the session's current generation without marking it
// dirty: the generation a commit starting right now would be committing.
func (t *Tracker) Latest() Generation {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.latest
}

// Accept reports the outcome of a commit attempt for generation, which
// must be a value previously returned by Dirty. When generation is
// stale - superseded by newer dirty input that arrived after the commit
// started - the completion is discarded: accepted is false and state is
// the empty LocalSaveState, and the caller must leave the session's
// currently displayed save state untouched. A failure for a generation
// that an earlier, different commit attempt has already confirmed saved
// is discarded the same way, so a late duplicate or superseded failure
// can never regress a confirmed save. Otherwise Accept returns the
// LocalSaveState the UI should now report: SaveStateSaved for succeeded,
// or SaveStateCouldNotSave otherwise, per specs/product-experience's
// closed three-value save-state model.
func (t *Tracker) Accept(generation Generation, succeeded bool) (state presentation.LocalSaveState, accepted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if generation < t.latest {
		return "", false
	}
	if !succeeded && t.hasSaved && generation <= t.saved {
		return "", false
	}
	if succeeded {
		t.saved, t.hasSaved = generation, true
		return presentation.SaveStateSaved, true
	}
	return presentation.SaveStateCouldNotSave, true
}

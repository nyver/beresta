package model

import (
	"errors"
	"fmt"
)

// ErrInvalidWorkspace reports a structurally invalid workspace record.
var ErrInvalidWorkspace = errors.New("model: invalid workspace")

// Workspace is the cryptographic sharing boundary for one notebook tree of
// notes. Each workspace is independently keyed; see core/crypto for the
// per-workspace key and envelope contract.
type Workspace struct {
	ID        ID
	CreatedAt HLC
}

// Validate rejects a structurally invalid workspace record.
func (w Workspace) Validate() error {
	if err := w.ID.Validate(); err != nil {
		return fmt.Errorf("%w: ID", ErrInvalidWorkspace)
	}
	if err := w.CreatedAt.Validate(); err != nil || w.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created clock", ErrInvalidWorkspace)
	}
	return nil
}

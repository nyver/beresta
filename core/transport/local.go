package transport

import "context"

// Local is the default synchronization transport: a deterministic no-op that
// never makes network requests and always reports itself as disabled. It
// lets every client run fully offline until a user explicitly configures a
// remote transport (see specs/sync-engine, "Local transport is the
// default").
type Local struct{}

// NewLocal returns the default local-only transport.
func NewLocal() *Local {
	return &Local{}
}

// Status always reports StatusDisabled: Local never reaches a remote peer.
func (*Local) Status(context.Context) Status {
	return StatusDisabled
}

var _ SyncTransport = (*Local)(nil)

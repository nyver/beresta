package transport

import "context"

// Status is the externally observable state of a synchronization transport,
// shared by every client's UI and status model (see
// specs/sync-engine and specs/windows-desktop-client).
type Status string

const (
	// StatusDisabled means no transport is configured; the client is
	// local-only and all changes stay queued for a future transport.
	StatusDisabled Status = "disabled"
	// StatusOffline means a transport is configured but currently
	// unreachable.
	StatusOffline Status = "offline"
	// StatusActive means the transport is reachable and synchronization is
	// in progress.
	StatusActive Status = "active"
	// StatusCurrent means the transport is reachable and there is no known
	// pending work.
	StatusCurrent Status = "current"
	// StatusFailed means the transport rejected requests in a way that
	// requires user attention (for example, revoked device credentials).
	StatusFailed Status = "failed"
)

// SyncTransport is the contract a workspace sync worker uses to exchange
// already-encrypted, already-signed operations and blobs with a remote sync
// peer (HTTP server, shared folder, or LAN peer). Implementations never see
// plaintext and never make authorization or CRDT-merge decisions; that
// belongs to the sync worker and domain code on either side of the
// transport. Additional methods (operation pull/push, blob transfer,
// snapshot exchange, cursor notifications) are added to this interface by
// the tasks that implement the pull-verify-apply-push worker and its
// concrete transports.
type SyncTransport interface {
	// Status reports the transport's current state for the shared sync
	// status model. It does not block on network I/O.
	Status(ctx context.Context) Status
}

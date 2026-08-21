# Phase 6 delivery report

## Delivered scope

Phase 6 adds the optional HTTP synchronization path without changing the
local-first storage model:

- canonical deterministic CBOR operation envelopes with strict decoding,
  protocol negotiation, resource limits, compatibility fixtures, fuzzing, and
  the exact signature input shared by client and server;
- a single cancellable worker per workspace with durable inbox, outbox,
  transport cursor, exactly-once ledger, quarantine journal, bounded batches,
  full-jitter backoff, WebSocket hints, and polling fallback;
- Ed25519, workspace-key, HLC, AEAD, Yjs, and metadata LWW verification before
  transactional application, including tombstones and deterministic flag,
  notebook, and tag behavior;
- TLS 1.3 HTTP transport with either SHA-256 certificate pinning or the Android
  and Windows system trust store, challenge refresh, late server attachment,
  and detach/re-attach without collection migration;
- resumable four-MiB encrypted blob chunks with durable verified checkpoints;
- signed encrypted operation-replay snapshots, active-device
  acknowledgements, safe 30-day compaction, and fresh-client bootstrap through
  the ordinary verification/apply path;
- desktop URL/invite/QR onboarding, diagnostics, sync state and actions,
  quarantine recovery, and device revocation UI.

## Review decisions

The snapshot representation is a bounded canonical `BSN1` operation archive,
not a second materialized-state schema. This makes bootstrap reuse operation
signature, AEAD, CRDT, LWW, exactly-once, and quarantine checks.

Snapshots are published immediately for the first contiguous cursor and then
after 1,000 additional operations. The creator acknowledges after upload;
other devices review the latest snapshot after reaching the current cursor.
This avoids the acknowledgement/compaction deadlock and keeps routine sync
from rebuilding a large snapshot after every edit.

The lead review also removed an unsafe timer reset that could cause a duplicate
immediate worker cycle, added per-cycle authenticated device-key refresh, and
preserved historical signature verification for operations accepted before a
device revocation.

## Verification

The requested test gate was respected: no tests ran until Android item 8.13
and its artifact build completed.

| Check | Result |
| --- | --- |
| Production server and Wails desktop builds | Pass |
| Go package suite, including account forced-termination, sync, transport, and server integration | Pass; one stale migration-count assertion was updated from two to three and the server package passed on rerun |
| Desktop frontend | 22 files, 134 tests passed |
| `go vet`, localization validation, TypeScript typecheck | Pass |
| 1,000-operation cursor/page benchmark | 4,331 ns/op, zero allocations on the Windows reference host; below the three-second budget |


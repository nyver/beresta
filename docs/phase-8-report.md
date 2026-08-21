# Phase 8 delivery report

## Delivered scope

Phase 8 completes sharing, revocation, key rotation, the optional folder
transport, and the production release pipeline (tasks 9.1-9.16):

- workspace membership sharing: per-recipient X25519 sealed key envelopes
  (`Account.ShareWorkspace` / `AcceptWorkspaceShare`), signed with the
  sharer's authority key over a canonical membership record, verified by the
  recipient before any local state changes;
- signed device and member revocation records (`SignDeviceRevocation`,
  `SignMemberRevocation`, `VerifyRevocation`) as a local audit trail
  alongside the server's own immediate enforcement boundary (revoking a
  device deletes its sessions/challenges and blocks new challenge issuance;
  revoking a member blocks its authorization checks on every workspace
  endpoint) - both already existed at the storage layer from earlier phases
  and are now exercised end to end;
- no-downtime workspace-key rotation (`BeginWorkspaceKeyRotation` /
  `AcceptWorkspaceKeyRotation`): a fresh key sealed to every remaining active
  member, published through a dedicated `PUT .../key-envelopes` call the
  server accepts only when every active member is covered, applied
  uniformly by every device (including the rotating one) through the same
  accept path a recipient uses;
- historical-key reads: `Account.workspaceKeyByID` resolves a decrypted
  object's embedded key ID against the current key first and then every
  retained historical key, so rotation does not break reading, editing, or
  re-exporting content written before it. This required extending
  `Account`'s in-memory workspace-key model (previously current-key-only)
  with a parallel historical-key map and updating every note-document read
  path (`loadNoteDocument` and its callers) to resolve through it;
- a resumable, purely local hardening job (`HardenWorkspaceKey`) that
  re-encrypts a device's own live note snapshots under the current key in
  bounded batches;
- local operation-log and snapshot garbage collection
  (`RunOperationLogGarbageCollection`): inbox/outbox/applied-operations rows
  and superseded snapshot catalog rows are collected once covered by a
  device-acknowledged snapshot at least the 30-day retention floor old,
  alongside the pre-existing tombstone/blob collector; `CreateWorkspaceSnapshot`
  and `ApplyWorkspaceSnapshot` now also record their snapshot in the local
  catalog (`saveSnapshotCatalogRow`), which nothing previously did;
- the optional folder transport (`core/transport.Folder`): immutable,
  sequence-numbered operation segments published via temp-write/fsync/rename,
  a short-lived manifest lock for sequence allocation with stale-lock and
  stale-temp-file recovery, and content-addressed blob chunk/manifest
  exchange with the same publish discipline;
- a `member-devices` server endpoint and client wiring
  (`Account.UpsertRemoteDevices`, already present from an earlier phase) so
  a workspace member's device can independently verify signatures from a
  fellow member's device - a gap the sharing end-to-end test surfaced
  immediately, since a shared workspace's operations are authored by a
  different account's device than the one applying them;
- release tooling: `cmd/beresta-release-sign` (signed desktop update
  manifest publication and generic detached-file signing), Android release
  signing wired into Gradle with a fail-closed check, a Windows batch
  launcher, and server cross-builds now publishing `SHA256SUMS`, a
  per-binary `go version -m` module manifest, and `provenance.json`;
- CI gates: a core coverage threshold (`coverage-gate`), `govulncheck` and
  OSV dependency scanning (`security-scan`), a short fuzz pass over the
  malformed-input decoders, and a tag-triggered signed-release-publish job.

## Review decisions

Key-transition distribution deliberately does not flow through the CRDT
operation log the way `docs/crypto-spec.md` originally sketched. It is a
direct, server-authorized REST call instead: the server already enforces
"every active member is covered" as a transactional invariant on that
endpoint, and mixing key material distribution into the same ordered,
CRDT-replayable stream as note content would gain nothing while adding a new
operation type every existing decoder would need to reject. `docs/crypto-spec.md`
and `docs/sync-protocol.md` were updated to describe the implemented design
rather than leave the earlier sketch in place as a misleading reference.

The folder transport approximates the HTTP transport's server-assigned
sequence model (a short-locked manifest allocates ranges) rather than the
order-independent op_id-merge model `docs/sync-protocol.md` originally
described, so every transport shares the same `coresync.Cursor`/`ApplyPage`
application path. This is validated for filesystems with immediate,
coherent cross-writer visibility (a local disk or LAN share); an eventually
consistent synced folder could let two writers race on the same sequence
range, which `Folder.Push` detects after the fact and reports as a
transient, retryable error rather than silently corrupting the sequence.

Historical-key reads required a small but real correctness fix beyond new
feature code: before this phase, `Account` could hold only one key per
workspace in memory, so a note written under a key that had since been
rotated out would fail to decrypt on the very next read - not merely
degrade. This was caught while building the rotation end-to-end test, not
by inspection, and is exactly the kind of gap the spec's "historical-key
reads" requirement exists to prevent.

## Verification

The requested test gate was respected: individual new/changed packages were
tested as they were written, but the complete suite and coverage gate ran
only after item 9.16.

| Check | Result |
| --- | --- |
| `core/account` sharing/rotation/revocation/hardening/GC tests | Pass |
| `core/transport` folder transport tests (including two-writer convergence) | Pass |
| `core/mobileapi` new `Service` facade lifecycle tests | Pass |
| `server` sharing/revocation/rotation end-to-end test against a real server, TLS, and independent accounts | Pass |
| `server` intercepted-traffic and data-directory opacity test | Pass |
| `server` hostile-server tampering rejection test | Pass |
| `server` push-batch atomicity test | Pass |
| `server` 1,000-operation LAN sync benchmark | Pass (664 ms measured, 3 s budget) |
| `core/store` 20,000-note / 150 ms search benchmark | Pass (re-verified) |
| Full repository build (`core`, `server`, `desktop`, `cmd/*`) | Pass |
| Full test suite and `coverage-gate` | Run after 9.16; see "Final review" below |

## Final review (9.16)

A lead-level pass over this phase's new code, focused on correctness,
concurrency, IDOR, cryptographic misuse, secret exposure, update safety, and
resource limits, found and fixed:

- **Correctness (concurrency race)**: `Account.AcceptWorkspaceShare`'s
  post-decrypt recheck treated *any* concurrently-installed key for the
  target workspace as success, including a genuinely different key than the
  one just decrypted. Fixed to compare the key ID, matching the equivalent
  (already-correct) check in `AcceptWorkspaceKeyRotation`.
- **Resource limits**: `Folder.Push` had no batch-size ceiling, unlike the
  HTTP transport. Added the same defensive cap (`maxFolderPushBatch = 256`);
  in normal operation `sync.Worker` already bounds batches before either
  transport sees them, so this only guards a caller that invokes `Push`
  directly.
- **Build hygiene**: `build/.go-tmp/` (this project's `GOTMPDIR`, containing
  transient compiled test binaries) was missing from `.gitignore` and would
  have been eligible to commit. Added.

No IDOR, cryptographic misuse, or secret-exposure findings survived review:
every new server endpoint reuses the existing `isActiveMember` /
`requireWorkspaceOwner` authorization pattern; every new signature uses a
distinct domain-separated `SignatureDomain` (verified by
`TestRevocationKindDistinguishesSignatureNamespace`); `cmd/beresta-release-sign`
only ever prints the paired *public* key; and the intercepted-traffic/data-
directory opacity test (this phase) found no plaintext, passphrase, or raw
key material anywhere the server can observe.

An environmental note, not a code finding: a `cmd/beresta-server/data/`
directory left over from a manual run before this phase carries a Windows
ACL that denies this session's own user account (via `Remove-Item`,
`takeown`, and `icacls /reset`, all attempted) - it needs to be deleted with
administrator elevation. Until then it blocks any `go` invocation using a
`./...`-style pattern from this repository root, including
`build.cmd build`/`package`'s Wails bindings-generation step; every Go
build/vet/test command in this report instead targeted the real package
list explicitly, which is unaffected by it.

Rerunning the complete release pipeline (`build.cmd verify`, `coverage-gate`,
`security-scan`) is recorded in this repository's commit history immediately
following this report's commit, per the requested sequencing (implement
9.1-9.16, commit, then run the full suite).

## Known limitations

- Desktop and mobile UI for initiating a share, viewing membership, or
  triggering rotation is not built. The underlying `core/account` and
  `core/transport` APIs are complete, tested, and independently callable;
  wiring them into the Wails/Flutter shells is unstarted UI work.
- The folder transport's two-writer convergence is verified on a local
  filesystem, not across every documented filesystem class (network share,
  FAT32, an eventually-consistent cloud-synced folder); the race-detection
  fallback (`ErrFolderRace`) is implemented and tested for the case it
  exists to catch, but not against a real slow-propagation filesystem.
- Desktop cold start, mobile cache/background behavior on physical hardware,
  and Raspberry Pi idle measurement were not re-run in this development
  environment, which has none of that reference hardware. Their acceptance
  harnesses are implemented and unchanged by this phase.
- Core test coverage was below the release-quality spec's 80% floor at the
  start of this phase; this phase added targeted coverage (most notably
  `core/mobileapi`, previously near-untested) but did not close the gap
  fully. See "Final review" below for the measured figure after 9.16.
- This development environment's disk I/O made `fsync`-heavy backup tests
  (`core/account`'s daily-backup rotation test in particular) slow enough to
  occasionally need a longer test timeout than Go's default; this reflects
  the local environment, not a code defect, and does not affect the
  durability guarantee `fsync` exists to provide.

# Beresta Synchronization Protocol

## Status

This document defines synchronization protocol version 1, identified as `beresta.sync.v1`. It is normative for client transport behavior, operation envelopes, cursor progression, snapshots, compaction, and encrypted blob transfer. Cryptographic inputs and primitives are defined in `crypto-spec.md`; authorization behavior is defined by the OpenSpec capabilities.

The protocol is offline first. A local commit is complete before synchronization begins. The optional server orders and relays opaque signed data but never decrypts or merges it.

## Design Properties

- Every client keeps a complete local copy of notes and metadata.
- The same client state machine works with local, HTTP, folder, and transient LAN transports.
- Operation application is idempotent by `op_id` and atomic with cursor persistence.
- Note bodies converge through a Yjs-compatible CRDT regardless of delivery order.
- Metadata converges through HLC-ordered LWW registers with a deterministic device-ID tie break.
- Server sequence numbers order retrieval only; they do not define note causality or merge winners.
- WebSocket messages are hints. Cursor-based pull is the source of truth.
- Snapshots are opaque, signed, encrypted, and acknowledged before destructive compaction.
- Attachment chunks are independently authenticated and resumable.

## Transport Contract

The core depends on this semantic interface:

```go
type SyncTransport interface {
    Push(ctx context.Context, workspaceID WorkspaceID, ops []Op) error
    Pull(ctx context.Context, workspaceID WorkspaceID, cursor Cursor) ([]Op, Cursor, error)
    PutBlob(ctx context.Context, workspaceID WorkspaceID, blob BlobUpload) error
    GetBlob(ctx context.Context, workspaceID WorkspaceID, blobID BlobID) (BlobDownload, error)
    Subscribe(ctx context.Context, workspaceID WorkspaceID) (<-chan Cursor, error)
}
```

Concrete APIs may expose paged or resumable helpers internally, but they must preserve these semantics. Cancellation stops network or filesystem work promptly and never advances durable state for an uncommitted batch.

### Local transport

The default local transport accepts no remote configuration, performs no network or shared-folder I/O, and reports synchronization as disabled. Local operations remain durable and are eligible for later publication when another transport is enabled.

### HTTP transport

The HTTP transport uses TLS 1.3, certificate pinning or explicitly configured public PKI, device-key challenge sessions, bounded REST requests, and WebSocket cursor hints. It is required for the optional home server.

### Folder transport

The optional folder transport publishes immutable operation segments and encrypted blobs through temporary write, flush, and atomic rename. A short lock protects only manifest or segment-number allocation. Readers ignore abandoned temporary files and consume complete segments idempotently.

### Transient LAN transport

Pairing establishes a SPAKE2-authenticated local channel. The existing device and new device exchange account bootstrap material and perform an on-demand history/snapshot/blob synchronization using the same validation and apply path. The pairing listener is not a persistent server.

## Canonical Encoding

Protocol structures use deterministic CBOR. The decoder rejects:

- duplicate map keys;
- non-minimal integer or length encodings in signed material;
- indefinite-length items;
- invalid UTF-8;
- trailing bytes;
- unknown mandatory fields;
- excessive nesting, collection counts, byte lengths, or total envelope size;
- integer overflow and invalid UUID byte lengths.

All signed inputs are reconstructed through the canonical encoder rather than signed over arbitrary received bytes. Limits are configuration values with safe defaults and hard implementation ceilings.

## Identifiers

| Value | Encoding | Property |
|---|---|---|
| `account_id` | 16-byte UUIDv7 | Opaque routing identifier |
| `device_id` | 16-byte UUIDv7 | Bound to one Ed25519 device key |
| `workspace_id` | 16-byte UUIDv7 | Visible routing and authorization scope |
| `op_id` | 16-byte UUIDv7 | Globally idempotent operation identifier |
| `object_id` | 16-byte UUIDv7 | Encrypted inside operation payload |
| `key_id` | 16 random bytes | Selects workspace key generation |
| `blob_id` | 32-byte HMAC-SHA-256 | Workspace-private attachment identity |
| `snapshot_id` | 16-byte UUIDv7 | Identifies one signed encrypted snapshot |

UUID sort order is an operational convenience only. Authorization and causality never rely on a UUID timestamp.

## Hybrid Logical Clock

The protocol encodes HLC as:

```text
hlc = {
    physical_ms: uint64,
    logical: uint32,
    device_id: bytes(16)
}
```

For a local event, the device compares wall-clock milliseconds with its persisted last HLC. It uses the larger physical value and increments the logical component when physical time does not advance. Before emitting an operation, the merged HLC is persisted in the same transaction as that operation.

For a received event, the client:

1. Rejects or quarantines values beyond the configured acceptable future-skew window.
2. Computes the standard HLC merge from local physical time, last local HLC, and received HLC.
3. Persists the merged clock in the same transaction as operation application.
4. Uses `(physical_ms, logical, device_id)` lexicographic order for metadata LWW comparison.

The server checks a broader configured HLC acceptance window to limit replay and future-time attacks, but does not use HLC to order its append-only sequence.

## Operation Envelope

### Visible envelope

An operation sent by a client has this canonical map:

```text
{
    protocol: "beresta.sync.v1",
    schema_version: 1,
    op_id: bytes(16),
    workspace_id: bytes(16),
    device_id: bytes(16),
    hlc: HLC,
    key_id: bytes(16),
    nonce: bytes(24),
    ciphertext: bytes,
    sig: bytes(64)
}
```

The client signature is defined in `crypto-spec.md`. The server verifies it against the current registered device key before assigning a sequence.

The server returns or stores the envelope with an additional `seq: uint64`. Sequence starts at 1 per workspace and increases by one for every newly accepted operation. A duplicate `op_id` with byte-identical signed content returns its original sequence. Reuse of an `op_id` with different content is rejected as a conflict/security error.

### Encrypted payload

The ciphertext contains:

```text
{
    payload_version: 1,
    mutation_kind: enum,
    object_id: bytes(16),
    crdt_update: bytes | null,
    metadata_updates: [MetadataRegisterUpdate],
    attachment_refs: [EncryptedAttachmentReference],
    tombstone: Tombstone | null,
    causal_context: bytes | null
}
```

`mutation_kind`, note/object ID, editor data, tags, notebook assignment, filenames, attachment metadata, tombstone details, and causal context remain encrypted. The server sees only the outer workspace/device/key routing fields, sequence, sizes, and timing.

An operation payload can contain a CRDT update, metadata updates, or both when they originate from one atomic local command. Unknown optional fields are retained or ignored according to the schema; unknown mutation kinds or required fields block application and quarantine the operation.

## Client Persistence State

Each workspace stores:

- current materialized notes and metadata;
- Yjs document state, state vectors, and accepted updates;
- applied `op_id` records;
- operation inbox/quarantine records;
- pending outbox operations and server acceptance state;
- durable pull cursor;
- last local/merged HLC;
- known key generations;
- blob transfer checkpoints;
- latest validated snapshot and acknowledgement state.

Materialized state, applied-operation record, merged HLC, and cursor update commit in one SQLCipher transaction. Outbox creation commits with the local mutation that produced it.

## Cursor Semantics

Protocol v1 uses a cursor with:

```text
cursor = {
    workspace_id: bytes(16),
    last_seq: uint64,
    epoch: uint32
}
```

`last_seq=0` means no sequenced operation has been applied. `epoch` changes only when a protocol-compatible server maintenance action invalidates previous cursor interpretation; ordinary compaction does not renumber sequences.

The HTTP API uses strict JSON containers and standard JSON base64 for byte
strings. UUIDs are canonical text only at this transport boundary. Before any
signature operation, clients and the server decode identifiers to raw bytes and
reconstruct the closed deterministic-CBOR structure defined under `schema/v1`;
JSON bytes are never signed directly. Clients treat a received cursor as
untrusted until its workspace/epoch and response ordering are validated. A
response cursor must equal the highest contiguous sequence represented by the
response or a documented empty-page boundary.

The client never advances its durable cursor before all operations through that cursor have authenticated and committed. A poison operation blocks contiguous progress and enters quarantine with an explicit recovery path; it is never silently skipped.

## HTTP Synchronization Surface

Paths are rooted at `/v1`. Exact request and response schemas live under `schema/`.

| Method and path | Purpose |
|---|---|
| `GET /auth/challenge` | Issue a short-lived single-use device challenge |
| `POST /auth/verify` | Verify Ed25519 proof and issue scoped session |
| `POST /auth/refresh` | Renew session with fresh device proof |
| `POST /sync/ops` | Push a bounded batch of signed encrypted operations |
| `GET /sync/changes?workspace_id=&cursor=&limit=` | Pull the next contiguous operation page |
| `GET /sync/stream?workspace_id=` | WebSocket cursor hints |
| `POST /blobs/init` | Declare encrypted blob manifest and reserve quota |
| `PUT /blobs/{blob_id}/chunks/{index}` | Idempotently upload one encrypted chunk |
| `POST /blobs/{blob_id}/complete` | Verify uploaded chunk set and atomically publish blob |
| `GET /blobs/{blob_id}` | Fetch encrypted manifest and available chunk state |
| `GET /blobs/{blob_id}/chunks/{index}` | Download one encrypted chunk |
| `PUT /blobs/{blob_id}/references/{reference_id}` | Idempotently add an opaque live reference |
| `DELETE /blobs/{blob_id}/references/{reference_id}` | Idempotently remove an opaque live reference |
| `POST /snapshots` | Publish a signed encrypted workspace snapshot |
| `GET /snapshots/latest?workspace_id=` | Fetch the latest snapshot for client verification and acknowledgement |
| `POST /snapshots/{snapshot_id}/ack` | Record active-device validation acknowledgement |

Every non-public endpoint authenticates the session and authorizes the resource through ownership or current workspace membership. The server does not infer authorization from a caller-supplied user ID.

## Pull, Apply, Then Push State Machine

One worker owns synchronization for a workspace. Triggers coalesce; they do not create concurrent workers for the same workspace.

```text
IDLE
  │ local pending work, timer, foreground, or cursor hint
  ▼
PULL ── transient failure ──► BACKOFF ── timer ──► PULL
  │ page received
  ▼
VERIFY_AND_APPLY ── invalid op ──► QUARANTINED
  │ atomic commit + cursor
  ├── more pages ────────────────► PULL
  ▼
PUSH ── transient failure ───────► BACKOFF
  │ accepted/deduplicated results committed
  ├── more pending ──────────────► PUSH
  ▼
BLOB_RECONCILE
  │
  ▼
WAIT_FOR_HINT_OR_TIMER
```

### Pull

The client requests operations strictly after its durable cursor in bounded pages. It verifies workspace, contiguous sequences, limits, canonical encoding, current device/account authorization records where applicable, signatures, key ID availability, AEAD, HLC, and `op_id` idempotency.

Each verified page applies in one transaction. Already applied identical operations are harmless. If the process terminates, restart observes either the old state/cursor or the complete new state/cursor.

### Push

After pull reaches the current server head, the client submits bounded outbox batches. The server transaction:

1. Authenticates and authorizes the current device/workspace.
2. Validates request limits, canonical envelope, signature, revocation boundary, HLC window, and quota.
3. Deduplicates `op_id`.
4. Assigns the next workspace sequence to each new operation.
5. Commits all accepted operations and sequences atomically.
6. Publishes a cursor hint after commit.

The response maps each `op_id` to accepted sequence, existing duplicate sequence, or stable rejection. The client marks only accepted/identical duplicate operations delivered. A permanent rejection remains visible in the conflict/quarantine journal.

New local operations created during push remain in the outbox and trigger another cycle.

### Retry

Transient network, timeout, `429`, and `5xx` failures use capped exponential backoff with full jitter. Useful progress resets the backoff. Authentication expiry obtains a new challenge once before normal backoff. Permanent validation, authorization, pinning, or unsupported-version errors do not spin; they change explicit synchronization status and require a defined recovery action.

## CRDT and Metadata Merge

Each note body is a Yjs-compatible document. The encrypted payload carries incremental Yjs updates; application is idempotent according to Yjs state vectors and the outer `op_id`. The local canonical Markdown projection is derived after a transaction and is used for FTS, export, diff presentation, and previews, not as the merge source.

Metadata fields are independent LWW registers. A candidate wins when its HLC tuple is greater than the stored tuple. Equal physical/logical clocks use device ID as the deterministic final component. Set-like tags are represented as per-tag registers so concurrent edits to unrelated tags do not overwrite the entire set.

Deletion produces a tombstone register. Older body or metadata operations cannot resurrect a note. Restore is a new signed operation with a later HLC and a new explicit state transition.

## Tombstones and Garbage Collection

Tombstones remain for at least 30 days. History or blobs reachable from a tombstoned object are collectible only when:

- retention time has elapsed;
- no retained backup requires them;
- all active devices have acknowledged a snapshot that includes the tombstone, or have been revoked under the compaction policy;
- reference and manifest verification succeeds;
- garbage collection records a dry-run plan before deletion.

An offline device returning within retention receives the tombstone and cannot resurrect older state.

## Workspace Snapshots

An authorized client creates a snapshot after applying a contiguous server cursor. Visible metadata is:

```text
{
    protocol: "beresta.sync.v1",
    snapshot_id: bytes(16),
    workspace_id: bytes(16),
    base_seq: uint64,
    cursor_epoch: uint32,
    key_id: bytes(16),
    creator_device_id: bytes(16),
    created_hlc: HLC,
    nonce: bytes(24),
    ciphertext_hash: bytes(32),
    ciphertext: bytes,
    sig: bytes(64)
}
```

The protocol-v1 encrypted snapshot contains a strict `BSN1` replay archive: a
bounded count followed by length-delimited canonical operation envelopes for
every sequence from 1 through `base_seq`. Replaying those authenticated
operations reconstructs CRDT states, metadata registers, tombstones, and
attachment references through the ordinary verify/apply path, avoiding a
second materialized-state decoder. Gaps, non-contiguous sequences, trailing
bytes, malformed envelopes, and a final sequence unequal to `base_seq` fail
closed.

### Validation and acknowledgement

The server verifies visible encoding, creator signature, current membership, key ID, size, and that `base_seq` exists, but cannot validate decrypted semantics. Another active authorized device downloads, decrypts, reconstructs, compares its state at `base_seq`, and signs an acknowledgement of `(snapshot_id, ciphertext_hash, base_seq)`.

The creator also records its acknowledgement. An acknowledgement is invalid after that device's authorization boundary changes unless the compaction decision already committed.

Clients review the latest snapshot after reaching the current operation
cursor. The creator acknowledges immediately after publication; other active
devices authenticate, decrypt, and replay-or-compare the archive before
signing. The latest endpoint therefore includes snapshots still collecting
acknowledgements; endpoint presence is not compaction eligibility.

### Compaction

The server marks a snapshot as the compaction base only when every active authorized device has acknowledged the same snapshot, or when missing devices have been explicitly revoked and the 30-day safety window has elapsed. It then retains:

- the accepted snapshot ciphertext and acknowledgement set;
- every operation with `seq > base_seq`;
- operations and blobs still protected by tombstone, backup, audit, or incomplete-transition retention.

Sequences are never renumbered. A pull cursor below the compacted boundary
returns stable `snapshot_required`; the client refreshes authenticated device
verification keys, fetches the accepted snapshot, verifies its creator
signature and workspace-key AEAD, replays only operations newer than its local
cursor in one transactional progression, signs an acknowledgement, sets the
cursor to `base_seq`, and pulls later operations.

## WebSocket Cursor Hints

After committing operations, snapshots, membership changes, or revocations, the server publishes a bounded in-process event. Authorized WebSocket subscribers receive only:

```text
{
    protocol: "beresta.sync.v1",
    workspace_id: bytes(16),
    latest_seq: uint64,
    cursor_epoch: uint32
}
```

Hints may be duplicated, coalesced, delayed, or lost. They carry no operation body and are never persisted as a client cursor. A client responds by scheduling ordinary pull. Slow subscribers are disconnected or have hints coalesced; they recover through polling.

## Encrypted Blob Transfer

The client first encrypts and verifies the attachment locally according to `crypto-spec.md`. The server-visible manifest includes workspace ID, private blob ID, key ID, encrypted manifest bytes, chunk count, encrypted chunk sizes/hashes, and total quota size.

### Upload

1. `init` authenticates/authorizes, validates limits, reserves quota, and returns already present chunk indexes.
2. The client uploads missing chunks by stable index. Repeating an identical chunk is successful; conflicting bytes for an existing index are rejected.
3. `complete` verifies the declared chunk set, sizes, encrypted hashes, quota reservation, and atomically publishes the blob metadata.
4. The client adds an opaque UUIDv7 reference after the local referencing operation is durable; retries are idempotent. It removes that same reference after the local tombstone/retention policy permits release.
5. Expired incomplete uploads are garbage collected and release quota.

The server validates encrypted hashes for transport integrity but cannot perform AEAD or plaintext blob-ID verification. The uploading client verifies those before upload, and every downloading client verifies them after download.

### Download

The client fetches the encrypted manifest, validates it, compares local verified chunks, downloads only missing chunks, and persists each verified encrypted chunk checkpoint. It authenticates every chunk, reconstructs the exact plaintext length, recomputes the workspace-private blob ID, and only then exposes the attachment.

Mobile lazy-download policy affects when chunks are fetched, not note metadata availability or synchronization correctness.

## Folder Segment Format

The implemented folder transport (`core/transport.Folder`) writes paths derived only from the hex-encoded workspace ID:

```text
workspaces/<workspace_id_hex>/
├── manifest.json
├── manifest.lock                          (transient)
├── segments/
│   ├── seg-<epoch>-<start_seq>-<end_seq>-<device_id_hex>.bin
│   └── .tmp-<random>                      (transient, pre-rename)
└── blobs/<aa>/<bb>/<blob_id_hex>/
    ├── manifest.json
    └── chunks/<index>.bin
```

Unlike the sketch this section originally described, the folder transport approximates the same server-assigned, strictly increasing per-workspace sequence the HTTP transport uses, rather than a pure op_id-merge model: a writer takes the manifest's short-lived lock file, reads `latest_seq`, claims the next contiguous range, and publishes one immutable segment (deterministic CBOR-encoded operations, length-framed, magic-prefixed) naming that exact range, via temp-write/fsync/rename. This keeps the same `coresync.Cursor`/`ApplyPage` machinery every transport shares, rather than requiring a separate order-independent apply path solely for this transport. Readers list finalized segment files (ignoring the `.tmp-` prefix), decode every operation whose sequence exceeds the caller's cursor, and sort by sequence.

This design trades a small liveness risk for that reuse: two writers whose view of the manifest is stale relative to each other (a filesystem with materially delayed cross-writer visibility, as an eventually-consistent cloud-synced folder might have) could both claim an overlapping range. `Folder.Push` detects that after the fact by rescanning for a colliding segment before trusting its own manifest update and reports `ErrFolderRace`, which the calling `sync.Worker`'s normal retry/backoff resolves on the next attempt; it is validated for directories with immediate, coherent visibility (a local disk or LAN share), which is this transport's documented target (see `core/transport/folder_test.go`'s two-writer convergence test).

A stale lock (its own writer crashed or lost access before releasing it) is recovered by age: a later writer removes and retakes a lock file older than the configured staleness window rather than waiting indefinitely. `Folder.PruneAbandonedTemp` removes orphaned `.tmp-*` publication files - segments and blob chunks alike - past their own staleness window; a finalized file never carries that prefix, so this is safe to run from any device sharing the folder at any time. Blob exchange mirrors the same discipline: each chunk and the blob manifest publish independently via temp-write/fsync/rename, and a chunk already present and hash-verified is not re-uploaded.

## Protocol Versioning

Every envelope contains a protocol identifier and schema version. Compatibility follows these rules:

- Readers ignore unknown explicitly optional fields after validating limits and canonical encoding.
- Readers reject unknown required fields, mutation kinds, cryptographic profiles, and major protocol identifiers.
- New writers do not emit a required version until all supported readers can consume it.
- Signature and AEAD domain strings include their version, preventing cross-version reinterpretation.
- Server API additions are backward compatible within `/v1`; incompatible routing or semantics require `/v2`.
- Stored operations remain immutable. Migration creates new snapshots or transition operations rather than rewriting signed history.
- A server may relay an opaque newer ciphertext only when it can still validate the visible envelope version, signature, authorization, and size limits. Otherwise it fails closed.

`schema/testdata` contains canonical positive vectors and malformed/compatibility cases for each supported version.

## Stable Error Classes

Transport and sync code classify errors so retry behavior is deterministic:

| Class | Retry behavior |
|---|---|
| `transient-network` | Backoff with jitter |
| `server-busy` | Respect bounded retry hint, then backoff |
| `session-expired` | One fresh device challenge, then backoff |
| `pin-mismatch` | Stop and require explicit trust recovery |
| `unauthorized` | Stop workspace sync and refresh device/membership state |
| `revoked` | Lock/self-wipe policy; never push pending operations |
| `quota-exceeded` | Stop affected upload and expose remediation |
| `invalid-operation` | Quarantine and block contiguous cursor |
| `unsupported-version` | Stop and require compatible software |
| `local-corruption` | Stop mutation/sync, preserve evidence, offer verified restore |

Errors and logs contain bounded opaque IDs and stable codes, never plaintext payloads, passwords, keys, authorization tokens, invite codes, or ciphertext excerpts.

## Required Verification

- Deterministic CBOR and signature compatibility vectors.
- Two or more replicas with randomized operation permutation, duplication, delay, snapshot, and replay.
- Concurrent body paragraphs and independent metadata/tag mutations.
- Cursor atomicity and injected termination before, during, and after page commit.
- Push retry and `op_id` deduplication across ambiguous network failure.
- Poison-operation quarantine without silent cursor advance.
- WebSocket loss/duplication/slow-consumer recovery through pull.
- Fresh-device bootstrap after acknowledged compaction.
- Revoked-device authentication and post-boundary operation rejection.
- Exact 4 MiB, multi-chunk, interrupted, duplicate, conflicting, and quota-limited blob transfers.
- Two-writer folder transport on every documented filesystem class.
- Late server attachment to an existing local-only database and clean return to local-only mode.

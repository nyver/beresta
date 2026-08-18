# Phase 2B Delivery Report

Date: 2026-08-18

## Scope

Phase 2B implements the encrypted local store and domain model on top of phase 2A's cryptographic core: the complete client SQLCipher schema, the account lifecycle, transactional note/notebook/tag repositories, the CRDT document adapter, local full-text search, an encrypted content-addressed blob store, and schema-migration safety. It does not yet wire any of this into a desktop or mobile user interface, connect to a synchronization transport, or implement backup/restore, export/import, or garbage collection (phase 3).

Delivered source includes:

- validated UUIDv7 identifiers, workspace/device/note domain models, Hybrid Logical Clock persistence, and deterministic last-writer-wins registers with device-ID and logical-counter tie breaks;
- the complete embedded client schema (accounts, devices, workspaces, notebooks, tags, notes, note metadata, CRDT state/updates, revisions, attachments/references, inbox/outbox, cursors, snapshots, backups, saved searches, FTS5) as transactional migrations;
- SQLCipher connection setup with WAL, foreign keys, busy timeout, integrity checks, and an OS-keystore-wrapped per-device database key;
- account creation/unlock/lock services that generate the identity, signing, device, database, and initial workspace keys without network access;
- transactional notebook trees, per-tag membership registers, note metadata/flags/tombstones, and saved queries;
- the `DocumentCRDT` adapter (rich text, canonical Markdown projection, state vectors, update application, snapshot/restore, malformed-update rejection) and atomic local note commands that update CRDT state, materialized metadata, FTS, an encrypted revision, and the signed outbox in one transaction;
- local full-text search (`SearchNotes`) with FTS5 bm25 title/body ranking, AND-semantics tag filters, inclusive date bounds, cancellation, a small saved-query filter language (`ParseSearchQueryText`), and a 20,000-note benchmark fixture enforcing the 150 ms release-quality budget;
- an encrypted content-addressed blob store (`BlobStore`) with write-temp/fsync/rename publication under the documented `<root>/<aa>/<bb>/<id>` layout, content-addressed deduplication, a fault-injection test seam proving no partial blob is ever visible after a terminated publish, and transactional `attachments`/`note_attachments` reference tracking with orphan grace-period timestamps;
- schema-migration safety: `Open` takes a whole-file backup before applying a pending migration to a pre-existing database, `RestoreDatabaseFile` is the tested forward-fix recovery path, and `RebuildFTSIndex` repairs the FTS5 index's own structures;
- broader property/opacity/recovery test coverage: randomized transaction-atomicity trials across every row kind, an HLC logical-counter tie-break test, two raw-byte "stolen storage" opacity tests (FTS-inside-SQLCipher and a whole profile directory including a real sealed attachment blob), and a WAL-replay recovery test simulating an unclean shutdown.

## Security and Design Decisions

**Forward-fix recovery, not per-migration down-migrations.** A SQL transaction already rolls back a migration that fails outright; the whole-file safety backup exists for the different case of a migration that succeeds at the SQL level but is later found to be logically wrong. A full-file restore recovers uniformly regardless of which migration was at fault, so no per-migration down-SQL is maintained. Backups are never auto-deleted; cleanup is left to a future operator/CLI task rather than guessed at here.

**Blob store dedup relies on content addressing, not locking.** `BlobStore.Publish` checks existence, then writes; a theoretical race between two concurrent publishes of the same content is unguarded, but is provably harmless because both publishers would write byte-identical content under the design's one-serialized-writer client model — there is no code path that publishes non-deterministic content under the same `BlobID`.

**`BlobStore.Open` does not defend against a symlink swap at the content-addressed path.** This is a documented, accepted gap: substituted content is still AEAD-sealed ciphertext keyed to the blob's identity, so a caller decrypting what `Open` returns detects the substitution during authentication rather than at the filesystem layer.

**Search excludes tombstoned notes by default, not by omitting them from the FTS index.** `notes_fts` retains a deleted note's row; `SearchNotes` filters on `notes.deleted` at query time. This keeps `ReplaceNoteFTS`'s existing delete-then-insert maintenance (task 3.7) untouched and makes an explicit `IncludeDeleted` search possible without a separate index.

## Verification Matrix

| Command | Scope | Result |
|---|---|---|
| `build.cmd verify` | Format check, locale check, `go vet`, gobind compile check, TypeScript typecheck, Flutter analyze, Go/Vitest/Flutter tests, Wails production build | Pass |
| `go test -race -count=1 ./core/store/... ./core/account/...` | Race-detector confirmation for the store and its only current consumer | Pass |
| `go test ./core/store/...` (no `-short`) | Full suite including the 20,000-note search benchmark fixture (`TestSearch20000NoteBudget`) | Pass (150 ms budget met) |
| `go test -coverprofile` on `core/store` alone | Package-local statement coverage | 74.9% |
| `go test -coverpkg=core/store/... ./core/store/... ./core/account/...` | Combined coverage including exercise through `core/account` | 77.1% |

The 80% core coverage gate is task 4.11's explicit requirement for phase 3, not phase 2B's; the figures above are reported as the honest current baseline, not a closed gate.

## Lead Review Findings

The review found and fixed the following issues before the final gate:

1. `backupBeforePendingMigration` queried `schema_migrations` before that table necessarily existed, failing every fresh-database `Open` call. Fixed by factoring table creation into a shared `ensureSchemaMigrationsTable` called by both `Migrate` and the new backup check.
2. An early WAL-sidecar-cleanup test asserted the absence of `-wal`/`-shm` files after reopening a connection, which itself recreates them as a normal side effect of WAL mode — the assertion was checking the wrong point in time. Fixed by moving the check to immediately after `RestoreDatabaseFile`, before any new connection is opened.
3. Adding migration `0002` changed the database's resulting schema version from 1 to 2; five existing hardcoded version assertions in `connect_test.go`/`migrate_test.go` were updated accordingly (an expected consequence of a new migration, not a functional regression).
4. A spot re-read of the pre-session CRDT/device-clock/revision repository code (`crdt.go`, `devices.go`, `revisions.go`) found no correctness, security, or resource-leak issues.

No other blocking correctness, performance, security, or UX findings were raised.

## Known Limitations

- The encrypted local store is not wired to any desktop or mobile screen; it is exercised only through Go tests and `core/account`.
- `RebuildFTSIndex` and `RestoreDatabaseFile` are tested primitives with no CLI or automatic caller yet; a future operator surface (task 6.13's server CLI, or an equivalent desktop path) is expected to invoke them.
- Pre-migration backup files accumulate under the profile root across app upgrades; no automatic cleanup exists yet.
- Attachment content itself (chunk framing, manifest assembly) is out of `core/store`'s scope; `BlobStore` only durably publishes and reads back opaque bytes a future attachment service (task 4.2) will supply already sealed via `core/crypto`.

## Phase Closure (3.12)

`README.md`, `config.example.yaml`, `docs/architecture.md`, `docs/threat-model.md`, and `ASSUMPTIONS.md` were checked against the delivered `core/store` source. `README.md`'s Project Status and Current Limitations sections were updated to describe phase 2B's delivered capabilities in place of the stale phase-2A description they still carried. `docs/architecture.md` and `docs/threat-model.md` gained short additions documenting the pre-migration safety backup. `config.example.yaml` and `ASSUMPTIONS.md` needed no changes: the former is explicitly server-only configuration (client storage internals have no server option, per its own header comment), and the latter's existing "Pre-migration and pre-restore safety backups are separate and do not consume daily slots" assumption already matched the delivered behavior.

Phase 2B is closed with all 3.1-3.12 tasks complete.

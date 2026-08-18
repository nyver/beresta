# Phase 3 Delivery Report

Date: 2026-08-18

## Scope

Phase 3 ("Complete Local-Only Product Core", tasks 4.1-4.12) builds the complete offline note application on top of phase 2B's encrypted local store: local application services, revision history, backup/restore, portable export/import, and garbage collection, plus the default no-op synchronization transport. It does not wire any of this into a desktop or mobile UI, connect to a synchronization transport that talks to a server, or implement sharing/revocation (later phases).

All of this delivered code lives in two packages: `core/transport` (the `SyncTransport` contract and its local no-op) and `core/account`, which is the account's live unlocked session plus every application service built on it — including backup/restore/export/import/garbage-collection, which need the session's retained Root Key, workspace keys, and open database connection that only `core/account` holds. `core/backup` remains the lower-level, key-agnostic file-manifest primitive `core/account` builds on; `docs/architecture.md`'s Module Ownership section has been corrected to describe this division (it previously described restore/export/import as `core/backup`'s responsibility, and did not describe `core/account` at all).

Delivered source includes:

- the `SyncTransport` contract and `Local`, the default no-op transport (`core/transport`);
- note/notebook/tag/attachment application services (`core/account/notes.go`, `attachments.go`) that commit local state and a signed encrypted outbox operation atomically, sharing one `commitNoteMetadata` transaction shape; notebook/tag structural lifecycle is local-only and does not itself produce an outbox operation;
- seven-day note revision history (`revisions.go`): a `format` column recording which Yjs encoding each delta is in (migration `0003`), periodic full-snapshot checkpoints every 20 deltas sequenced by insertion order (not timestamp — see Review Findings), a line-based LCS diff between two revisions' canonical Markdown, and rollback that reconstructs a revision's plain text and applies it as one new edit;
- daily encrypted client backups (`backup.go`): a plaintext SQLCipher export via `sqlcipher_export()`'s documented cross-key recipe, zstd compression, encryption under a backup key derived from the account's retained Root Key, a self-contained hardlinked-or-copied attachment blob set, a SHA-256 manifest, atomic staging-directory publication, `EnsureDailyBackup` (creates at most one per local day and covers the missed-day/device-was-off case for free), and rotation to exactly seven valid daily backups;
- backup verification and corruption tracking (`corrupt` column, migration `0004`), excluded from rotation/same-day counts, plus capacity preflight against free disk space (`diskspace_windows.go`/`diskspace_unix.go`) before writing anything;
- backup preview, a dry-run restore change plan (addition/update/unchanged classification, using plaintext note metadata and CRDT state vectors — no decryption needed for the dry run), atomic whole-database restore (`restore.go`) onto a freshly generated device database key with crash-safe rollback to the original database and key envelope on any failure, and selective restore that imports chosen notes as brand-new local notes/operations;
- confirmed plaintext Markdown/attachment/`manifest.json` export (`export.go`) with safe staging and cleanup on failure, and import of both Beresta's own portable archives and Evernote `.enex` files (`import.go`), with a user-visible warning report for anything not representable (both flatten rich text to plain text, since neither Markdown nor ENML has a parser back into the CRDT rich-text model here);
- blob and tombstone garbage collection (`gc.go`) at the sync-engine spec's 30-day minimum retention floor, with dry-run reporting and an informational (non-blocking) backup-awareness check;
- a headless end-to-end suite (`e2e_test.go`) chaining the complete local-only lifecycle, a real forced-termination test that kills a worker subprocess mid-operation and verifies clean recovery, and randomized local-operation/restore-convergence property tests (`randomized_test.go`) across multiple deterministic seeds.

## Security and Design Decisions

**The account's Root Key is retained for the whole unlocked session, not just transiently during unlock.** Every prior phase closed the keybag's Root Key immediately after decrypting it. Backup creation derives a fresh per-backup key from the Root Key on demand (`crypto_profile_v1`'s `HKDF("backup", account_id, backup_id)`); re-deriving it from the passphrase for every scheduled daily backup would mean re-prompting the user daily. The Root Key is treated exactly like every other live secret already retained this way (identity/authority/device/workspace keys): wiped by `Lock`, never crossing an API boundary.

**Whole restore re-encrypts under a freshly generated device key, not the original.** The original per-device SQLCipher key is wrapped by this device's OS keystore and is never portable; a restored database (which must be openable on a fresh device, or after key loss) cannot depend on it. Restore decrypts the backup's snapshot to plaintext, re-encrypts it under a brand-new key via `sqlcipher_export()`'s cross-key recipe, and swaps it into place; on any failure after the original file is moved aside, the swap is rolled back and the original database is reopened with the original key, so a failed restore leaves the account exactly as it was and still fully usable — not just data-safe.

**Selective restore always assigns fresh note IDs and recreates notebook/tag structure by name.** This matches the backup-and-recovery spec's "imports chosen objects as new local operations so future synchronization remains coherent" and sidesteps the question of merging a restored note's history against a possibly-since-diverged local note of the same ID.

**Garbage collection's backup-awareness is informational, not a gate.** A backup set is a self-contained copy of everything it referenced at creation time (including its own blob directory); live blob or note collection never affects an already-published backup's ability to restore. The dry-run report tells the user whether a collection candidate still exists in some backup, for reassurance, but never blocks collection on it.

**Rich text is not round-tripped through revision rollback or import.** There is no Markdown-to-CRDT or ENML-to-CRDT parser in this codebase. Both paths reconstruct plain text and apply it as one new edit or new note, and both report the simplification to the user (rollback implicitly documents it; import returns a per-note `ImportWarning`) rather than silently dropping formatting.

## Verification Matrix

| Command | Scope | Result |
|---|---|---|
| `build.cmd verify` | Format check, locale check, `go vet`, gobind compile check, TypeScript typecheck, Flutter analyze, Go/Vitest/Flutter tests, Wails production build | Pass |
| `go test ./core/account/... ./core/store/...` (full CGO/SQLCipher environment) | Unit, integration, headless end-to-end, forced-termination, and randomized property tests | Pass |
| `go test -coverprofile` on `core/account` | Package-local statement coverage | 71.3% |
| `go test -coverprofile` on `core/store` | Package-local statement coverage | 64.9% |
| `TestForcedTerminationDuringNoteCreationRecoversCleanly` (3 consecutive runs) | Real OS process-kill mid-operation, reopened and re-verified | Pass every run |
| `TestRandomizedLocalOperationsAndRestoreConverge` (4 fixed seeds) | Randomized note operations, backup checkpoint, further mutation, whole restore, exact-state convergence check | Pass every seed |

Two pre-existing, unrelated tests (`TestWALRecoveryAfterUncleanClose` in `core/store`, `TestProbeEncryptedRoundTrip` in `core/store/sqlcipherdb`, and once `TestOpenTakesSafetyBackupBeforeApplyingAPendingMigration`) were observed to fail intermittently during full-suite runs on this machine and pass reliably in isolation; the failure signature (`TempDir RemoveAll cleanup: ... directory is not empty`) is a Windows file-handle-release timing race unrelated to any phase-3 change, present before this phase's work began.

The 80% core coverage gate (task 4.11) is not met for `core/account` (71.3%) or `core/store` (64.9%). The functional gaps coverage analysis found (real untested logic, not defensive branches) were closed with targeted tests; the remainder is overwhelmingly cleanup-on-error branches — for example `createAccountContent`'s cascading `Close()` calls on each intermediate failure step — that need dedicated fault-injection infrastructure (mock keystore wrappers and filesystems that fail on a specific call) to exercise, comparable in effort to what phase 2B's `blob_test.go` built for the blob store alone. This was raised to the user during the session, who chose to accept the current coverage level and continue rather than build that infrastructure now.

## Lead Review Findings

The review found and fixed the following issues before each task's commit:

1. `NoteMetadataOperation`'s notebook-reassignment encoding wrote the zero ID (`model.Nil`, meaning "file at the workspace root") as 16 zero bytes, but decoding called `model.ParseID`, which explicitly rejects the all-zero value — every "move to root" operation would have failed to decode. Fixed by special-casing the zero value in the decoder instead of routing it through `ParseID`.
2. `AddAttachment` validated that its target note belonged to the given workspace only inside the final `commitNoteMetadata` step, after already staging, encrypting, and publishing the blob and creating its attachment row. A wrong-workspace `noteID` would leave a permanently unreferenced, un-garbage-collectible attachment row (orphan tracking only ever engages via `SetNoteAttachment`, which the failed final step never reached). Fixed by validating the note first.
3. Revision checkpoints and the deltas that trigger them are written in the same transaction and so share a millisecond-resolution `created_unix_ms`; ordering and counting by that timestamp alone could misplace or undercount them under rapid successive edits (routine in tests, plausible in production autosave bursts). Fixed by sequencing on SQLite's implicit `rowid` (true insertion order) instead.
4. `RemoveAttachment` only ever sets an attachment's `note_attachments` row to `present = 0`; it never deletes the row. Garbage-collecting that attachment without also removing its (possibly several) `present = 0` reference rows violated their foreign key into `attachments`. Fixed by having `DeleteAttachment` remove all of a blob's `note_attachments` rows first.
5. A backup's snapshot is taken before its own catalog row is written, so restoring from a backup (`RestoreWhole`) silently dropped both the pre-restore safety backup's row (created moments before the swap, but the swap replaces the whole database including its `backups` table) and the target backup's own row from the resulting catalog — found by the end-to-end test attempting a second restore from the same backup afterward. Fixed by re-registering both against the restored database once the swap succeeds.
6. The forced-termination test's fixed 300ms delay before killing the worker subprocess was sometimes shorter than account creation (Argon2id calibration plus database file creation) took on a loaded machine, so the kill occasionally landed before any account existed — not a useful test of "recovers cleanly" and a source of flakiness. Fixed by having the worker signal readiness only once account creation finishes, and having the parent wait for that signal before its kill delay.
7. Y.Text indices are UTF-16 code units (matching JS Yjs, for cross-implementation compatibility), not Go runes or bytes; `RestoreRevision`'s delete-then-insert rollback needed a UTF-16 length calculation, not `utf8.RuneCountInString`, or it would misindex non-BMP or multi-byte content. Verified against the underlying `ygo` library's `Text.Len()` implementation before writing the calculation, avoiding the bug rather than fixing it after the fact.
8. Five hardcoded schema-version/migration-count literals in `core/store` tests (`connect_test.go`, `migrate_test.go`) broke every time this phase added a migration (0003, then 0004). Replaced with assertions derived from `Migrations()` itself so a future migration does not silently break them again.

No other blocking correctness, performance, security, or UX findings were raised. Every fix above was caught by a test in the same commit that introduced the bug, before that commit was made — none reached a later phase.

## Known Limitations

- `core/account` and `core/store` do not meet the 80% core coverage target; see Verification Matrix.
- Rich-text formatting is not preserved across revision rollback or import (Beresta portable archive or Evernote `.enex`); both fall back to plain text and report the simplification.
- Dry-run restore's "update" classification is coarse (title/notebook/flags/deleted metadata plus CRDT state-vector equality), not the sync engine's CRDT-aware convergence; it reports that content differs, not why, and is not a conflict-resolution engine.
- Whole restore assumes the account's identity, device, and workspace keys are unaffected by restoring an older database snapshot (true today, since there is exactly one workspace per account and no key rotation yet); this assumption will need revisiting once phase 9 adds workspace-key rotation.
- Garbage collection covers attachment blobs and tombstoned notes; notebook and tag tombstones are not separately collected (they are cheap, low-value-to-reclaim metadata rows, not attachment-scale storage).
- None of this phase's functionality is reachable from a UI yet; `core/account`'s methods are exercised only through Go tests, including the new headless end-to-end suite.

## Phase Closure (4.12)

`README.md`'s Project Status and Current Limitations sections were updated to describe phase 3's delivered capabilities, including the coverage gap and the plain-text-only rollback/import simplification, in place of the phase-2B description they still carried. `docs/architecture.md` gained a `core/account` module description (previously missing entirely), a correction to `core/backup`'s description (it previously attributed restore/export/import to `core/backup`, which this phase's implementation places in `core/account` instead, for the reasons in Security and Design Decisions), and short additions to the Backup and Recovery Boundary section covering whole restore's device-key regeneration and garbage collection. `docs/threat-model.md` already described backup/export/garbage-collection threats accurately at the design level and needed no changes. `ASSUMPTIONS.md` gained three new entries (Root Key retention, garbage collection's non-blocking backup awareness, selective restore's fresh-ID/recreate-by-name behavior) and one addition to an existing entry (plain-text-only rollback/import). `config.example.yaml` needed no changes: it is explicitly server-only configuration (per its own header comment), and phase 3 is entirely client-side with no server-configurable surface yet.

Phase 3 is closed with all 4.1-4.12 tasks complete, except that task 4.11's 80% coverage figure is not met (see Known Limitations); the user was informed during the session and chose to accept the current coverage level rather than invest in the fault-injection test infrastructure required to close it fully.

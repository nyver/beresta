# ADR 0005: Use SQLCipher and Encrypted Content-Addressed Blobs on Clients

- Status: Accepted
- Date: 2026-08-16

## Context

Each client stores a complete local copy, searchable decrypted projections, CRDT state, operation history, backups, and up to 20 GB of attachments. A copied locked-device directory must not expose note content, metadata, FTS terms, or attachments. The Go core must own repository and migration semantics on Windows and Android.

## Decision

Use SQLCipher with AES-256-compatible full-database encryption, WAL mode, foreign keys, busy timeout, integrity checks, and transactional embedded migrations. Keep the FTS5 index inside the encrypted database. Generate a random database key per device and wrap it with DPAPI/Windows Hello or Android Keystore.

The Go core owns SQL schema, transactions, queries, FTS projection, and migration logic. A build-tagged adapter links a pinned audited SQLCipher amalgamation on Windows and Android. If a target cannot safely expose SQLCipher through the common `database/sql` adapter, an ADR may select the official platform SQLCipher binding behind an equivalent core-owned storage RPC. Plaintext SQLite and selected-column-only encryption are not valid fallbacks.

Store attachments as independently encrypted immutable files under `blobs/<aa>/<bb>/<blob_id>`. Publish by temporary write, flush, atomic rename, then database reference commit. Collect verified unreferenced blobs only after a grace period and retention checks.

## Consequences

- Notes, metadata, revisions, operation logs, and search terms share one encrypted transactional boundary.
- Native SQLCipher packaging is a required Windows/Android feasibility and release gate.
- WAL and filesystem blobs span two durability domains, so ordering and crash-injection tests are mandatory.
- Per-device database keys avoid synchronizing local storage keys and permit device-local wipe.
- Derived FTS state can be rebuilt and is never treated as canonical backup or sync state.

## Rejected Alternatives

### Plain SQLite with application-level encryption only for note bodies

Rejected because metadata, indexes, filenames, operation structure, and deleted content could remain visible on disk.

### One encrypted file per note without SQLite

Rejected because transactional metadata, FTS, migrations, operation idempotency, and crash recovery would need a custom database.

### Pure-Go SQLite without SQLCipher on clients

Rejected because it does not provide the required full-database SQLCipher encryption. Pure-Go SQLite remains appropriate for the opaque server.

### Store attachments as database blobs

Rejected because large attachments make backup, lazy download, resumable transfer, and content-addressed deduplication less efficient and complicate database growth/recovery.

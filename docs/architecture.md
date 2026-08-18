# Beresta Architecture

## Status

This document defines the target architecture for Beresta. It is normative for module boundaries and trust assumptions. User-visible behavior is specified in OpenSpec; cryptographic and synchronization wire details are defined in `crypto-spec.md` and `sync-protocol.md`.

Beresta is designed for a household deployment of up to five users, one to three devices per user, 20,000 notes, and 20 GB of attachments per user. The design deliberately excludes infrastructure and abstractions intended only for larger installations.

## Architectural Principles

1. A client is a complete application. Account creation, note editing, attachments, search, revision history, backup, restore, import, and export work without a server.
2. Local persistence commits before user-visible success. Network synchronization is asynchronous replication and never gates editing.
3. The optional server is an untrusted transport and ordering service. It never receives passwords, plaintext content, plaintext search terms, or unwrapped workspace keys.
4. The security-sensitive domain, cryptography, local persistence semantics, backup behavior, and synchronization engine are implemented once in Go.
5. Platform code is limited to UI, lifecycle, keystore and biometric access, background scheduling, file selection, and operating-system integration.
6. Every persistent and wire format is explicitly versioned. Derived projections such as FTS indexes can be rebuilt from canonical encrypted state.
7. SQLite transactions and immutable blob publication provide crash safety. No operation is partially visible after restart.

## System Context and Trust Boundaries

```text
┌──────────────────────────────── Client Device ────────────────────────────────┐
│ Trusted while unlocked                                                        │
│                                                                               │
│  Windows Wails/React UI or Flutter UI                                         │
│                    │ validated application commands                           │
│                    ▼                                                          │
│  Shared Go core: model, crypto, store, backup, sync, transports               │
│          │                         │                         │                  │
│          ▼                         ▼                         ▼                  │
│  OS keystore/biometrics      SQLCipher + FTS5       encrypted blob directory  │
└───────────────────────────────┬────────────────────────────────────────────────┘
                                │ TLS 1.3 + encrypted signed payloads
                                ▼
┌──────────────────── Optional Untrusted Home Server ───────────────────────────┐
│ Go HTTP/WebSocket service                                                     │
│          │                                      │                             │
│          ▼                                      ▼                             │
│ SQLite opaque metadata/log                 encrypted blob directory           │
└────────────────────────────────────────────────────────────────────────────────┘
```

The client-device boundary protects decrypted content and live keys. Disk theft is addressed by SQLCipher, encrypted attachments, and an OS-keystore-wrapped database key. A fully compromised unlocked client is outside the recoverable threat boundary because it can observe plaintext that the user is actively viewing.

The network and server are hostile boundaries. TLS 1.3 authenticates and protects the transport, including pinned self-signed certificates for home deployments. Independent authenticated payload encryption prevents a malicious or compromised server from reading or modifying application content undetected.

The UI is not trusted to construct storage or wire objects directly. It submits typed commands to the core and receives presentation values and explicit state events. Remote DTOs are decoded, bounded, authenticated, and converted into validated domain values before they reach repositories.

## Module Ownership

### `core/model`

Owns validated identifiers, workspaces, devices, notes, notebooks, tags, revisions, metadata registers, Hybrid Logical Clock values, commands, and domain errors. It has no dependency on UI, HTTP, SQLite drivers, or platform APIs.

### `core/crypto`

Owns the versioned cryptographic profile, key derivation, keybags, content encryption, attachment manifests, signatures, sealed workspace-key envelopes, and explicit key-buffer lifetimes. It depends only on audited Go cryptographic packages and the operating-system CSPRNG.

### `core/store`

Owns client migrations, transaction boundaries, repositories, operation inbox/outbox, cursor durability, FTS5 projections, and content-addressed encrypted blob publication. SQLCipher adapters are selected by platform build tags. The store does not perform network I/O.

### `core/sync`

Owns operation encoding and validation, HLC merge rules, the Yjs-compatible CRDT adapter, metadata LWW resolution, idempotent apply, retry state, snapshots, and compaction eligibility. It performs all content merge work on clients.

### `core/transport`

Defines the `SyncTransport` boundary and implements local, HTTP, folder, and transient LAN-pairing transports. A transport moves opaque signed objects and does not interpret note contents.

### `core/backup`

Owns client backup manifests, encrypted snapshot creation, seven-day retention, integrity checks, restore planning, selective restore, import, and export. It operates independently of synchronization.

### `core/mobileapi`

Exposes coarse, value-oriented methods and bounded event delivery that `gomobile bind` can safely export. It does not expose database handles, Go callbacks from arbitrary threads, or live key buffers.

### `desktop`

Contains the Wails process host, React/TypeScript UI, Windows Hello and DPAPI adapters, global hotkey, tray, autostart, clipboard, installer, and signed updater. Go core application services are the only persistence interface exposed to React.

### `mobile`

Contains the Flutter UI and Android platform integrations for Keystore, biometrics, secure screen flags, WorkManager, document providers, share targets, app widgets, and private-storage handoff.

### `server`

Contains the optional `net/http` and chi service, Ed25519 device authentication, invite enrollment, authorization, opaque synchronization log, blob transfer, WebSocket cursor notifications, administration CLI, and server backups. It uses cgo-free `modernc.org/sqlite` and never imports client decryption packages.

### `schema`, `docs`, and `build`

`schema` owns normative format descriptions and compatibility fixtures. `docs` owns architecture, threat, crypto, sync, and ADR records. `build` owns reproducible local verification, packaging, CI, and deployment assets.

## Dependency Direction

Dependencies point inward toward domain contracts:

```text
desktop / mobile / server
          │
          ▼
application services and transport adapters
          │
          ▼
core/model ← core/crypto ← core/store / core/sync / core/backup
```

The exact package graph can differ where a service needs both model and crypto, but these constraints remain:

- Core packages never import desktop, mobile, or server packages.
- The server never imports client plaintext repositories, search, editors, or key-unwrapping workflows.
- Transport implementations depend on the transport contract; sync does not branch on concrete transport type.
- UI code cannot issue SQL, create cryptographic nonces, or construct signed operations.
- Generated bindings and wire DTOs remain at adapters and are converted before domain use.
- Cross-cutting configuration is passed explicitly; packages do not read global environment state during normal operation.

## Local Storage Topology

Each client profile has one private data root:

```text
profile/
├── beresta.db                 # SQLCipher database in WAL mode
├── beresta.db-wal
├── beresta.db-shm
├── blobs/<aa>/<bb>/<id>       # encrypted immutable attachment chunks/manifests
├── backups/                   # encrypted, verified client backup sets
└── runtime/                   # replaceable locks and temporary files
```

The database contains canonical note metadata, CRDT updates/state, revisions, operation inbox/outbox, cursors, encrypted keybag data, backup catalog, blob references, saved queries, and an FTS5 index inside SQLCipher. The local database key is random per device and wrapped by the platform keystore. Account and workspace keys are separately protected by the encrypted keybag.

Blob publication follows write temporary file, flush, atomic rename, then database reference commit. A crash can therefore leave an unreferenced immutable blob, which delayed garbage collection can remove, but cannot leave a committed reference to a partial blob.

Opening the database takes a whole-file safety backup (`beresta.db.pre-migration-v<from>-<timestamp>.bak`, beside `beresta.db`) before applying a pending schema migration to a pre-existing database; a fresh database and one already at the latest schema skip this. These backups are never auto-deleted and are the forward-fix recovery path if a migration is later found to be wrong.

The optional server data root is intentionally simple:

```text
data/
├── beresta.db
├── blobs/<aa>/<bb>/<id>
├── config.yaml                # optional
└── tls/                       # generated or configured certificate material
```

Copying this directory captures all server state, but content remains opaque without client-held keys.

## Offline-First Command Lifecycle

1. The UI sends a validated intent such as editing a note or attaching a file.
2. The core validates identifiers, limits, account lock state, and workspace access.
3. The CRDT or metadata register produces a deterministic local mutation.
4. The core encrypts and signs an operation and updates current materialized state, revision history, FTS projection, and outbox in one SQLite transaction.
5. The client reports success after the local transaction commits.
6. A background worker later pulls remote operations, validates and atomically applies them, then pushes pending local operations.
7. Transport failure changes synchronization status and retry scheduling only; it does not roll back or block local work.

Local-only mode uses the same lifecycle with a no-op transport. Enabling HTTP synchronization later publishes existing encrypted history without migrating the database. Disabling it preserves the full local collection and pending outbox.

## Synchronization and Conflict Ownership

Note bodies use a Yjs-compatible CRDT and converge regardless of operation delivery order. Titles, notebook assignment, tags, and flags use HLC-ordered LWW registers with a deterministic device-ID tie break. Deletes are tombstones retained for at least 30 days.

Clients own decryption, validation, CRDT application, metadata merge, snapshot creation, and conflict/quarantine UX. The server assigns per-workspace sequence numbers, deduplicates operation IDs, stores opaque objects, enforces membership and quotas, and sends cursor hints. It cannot merge content.

Encrypted snapshots become compaction boundaries only after all active authorized devices acknowledge the same signed snapshot, or after explicit revocation and retention conditions. The server retains the accepted snapshot and later operations so a new device can bootstrap after compaction.

## Backup and Recovery Boundary

Client backups are the primary recovery mechanism and work without a server. A backup contains a consistent SQLCipher snapshot plus all referenced encrypted blobs, compressed and then authenticated-encrypted under a backup key. Integrity manifests are checked at startup and before restore. Pre-migration and pre-restore safety snapshots do not consume the seven daily slots.

Whole restore validates in a temporary location before atomic replacement. Selective restore imports chosen objects as new local operations so future synchronization remains coherent. Plaintext export is separate from backup and requires an explicit warning and confirmation.

Server backups protect availability of opaque synchronization state only. They cannot recover user content without a client keybag and passphrase.

## Runtime and Deployment Model

The Windows client is a Wails v2 executable using the installed WebView2 runtime and an NSIS installer. The Android client is a Flutter application linked to an AAR binding produced from the same Go core. The server is one static binary for Windows amd64, Linux amd64, and Linux arm64.

No deployment requires PostgreSQL, Redis, S3-compatible storage, a message broker, containers, or Kubernetes. Optional systemd, Windows Task Scheduler, and container assets wrap the same single process and data directory.

## Change Control

Changes to trust boundaries, cryptographic primitives, schema compatibility, client authority, server opacity, or fixed technology choices require an ADR. An ADR must record the technical constraint, alternatives, security impact, migration and rollback behavior, and compatibility consequences before implementation proceeds.

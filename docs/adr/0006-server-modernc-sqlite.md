# ADR 0006: Use cgo-Free SQLite and a Local Blob Directory on the Server

- Status: Accepted
- Date: 2026-08-16

## Context

The optional home server must run as one static binary on Windows amd64, Linux amd64, and Linux arm64, including Raspberry Pi. It supports at most five users and stores only opaque encrypted synchronization objects. External databases, object stores, caches, queues, and orchestrators are prohibited.

## Decision

Use `modernc.org/sqlite` with embedded goose migrations, WAL, `busy_timeout=5000`, `synchronous=NORMAL`, foreign keys, one serialized writer, and short read transactions. Store users, devices, memberships, envelopes, operation metadata/ciphertext, blob references, and snapshots in `beresta.db`.

Store encrypted blob chunks as ordinary immutable files under `blobs/<aa>/<bb>/<blob_id>`. Use temporary write, flush, atomic rename, and transactional metadata publication. Use bounded in-process Go channels for WebSocket cursor hints and a bounded in-memory rate limiter.

All persistent server state lives under one configured data directory. Direct binary execution is the primary deployment path. Systemd, Windows Task Scheduler, and a minimal container are optional wrappers around the same binary and directory.

## Consequences

- The server cross-compiles without cgo or a runtime database installation.
- One household-sized SQLite writer is simpler than a distributed consistency system and has ample capacity for the target load.
- The server cannot scale horizontally and intentionally provides no high-availability database layer.
- Database and blob backup consistency needs verified manifests and hardlink-or-copy snapshot behavior.
- In-process pub/sub notifications are ephemeral; cursor polling remains authoritative after restart or disconnect.

## Rejected Alternatives

### PostgreSQL

Rejected because installation, credentials, upgrades, backups, and service supervision violate the single-binary home-server objective and are unnecessary at the fixed scale.

### S3 or MinIO

Rejected because encrypted blob files need no object-service semantics and an external service increases operational and backup complexity.

### Redis, message broker, or job scheduler

Rejected because WebSocket hints and one background compaction/backup worker fit safely in one process. Durable state remains in SQLite.

### Kubernetes or horizontal sharding

Rejected because five users are the design ceiling. These mechanisms add failure modes without serving a supported requirement.

### cgo SQLite driver

Rejected because it complicates static Windows/Linux/ARM cross-compilation. Client SQLCipher's native requirement does not apply to the opaque server database.

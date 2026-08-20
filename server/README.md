# Beresta server

This directory contains the optional single-binary home synchronization server.
It implements invite-only enrollment, device-key sessions, resource-scoped
authorization, versioned keybags, workspace membership, signed operation
ordering, resumable encrypted blobs, signed snapshots, cursor WebSockets,
administrative commands, and verified backups. SQLite and the blob filesystem
store only opaque client ciphertext.

Initialize and verify state without opening a listener:

```powershell
go run ./cmd/beresta-server --data ./data --init-only
```

Start the TLS 1.3 listener:

```powershell
go run ./cmd/beresta-server --data ./data
```

The first run creates this layout:

```text
data/
|-- beresta.db
|-- blobs/
|-- backups/
`-- tls/
    |-- server.crt
    `-- server.key
```

The startup log prints the certificate's colon-delimited SHA-256 fingerprint.
Verify it over a trusted channel before a client pins it. Authentication proofs
are bound to this fingerprint. A partial TLS identity fails closed instead of
silently replacing a key. Health is available at `/health`; the versioned API is
documented in [`docs/server-api.md`](../docs/server-api.md).

By default the server reads `<data>/config.yaml` when it exists. Use `--config`
for another path; `--data` always overrides `server.data_dir`. Unknown YAML
fields, multiple YAML documents, sessions longer than 24 hours, non-4 MiB chunk
sizes, unsupported log formats, invalid metrics listeners, and unsupported TLS
modes are rejected.

Unix data paths are restricted to owner-only modes. On Windows, the data root
uses a protected ACL granting full control only to the current user and Local
System, with inherited protection for generated files.

Administrative commands, backup/restore safety, systemd, Windows scheduled
start, the optional container, cross-builds, and Raspberry Pi measurement are
documented in [`docs/server-operations.md`](../docs/server-operations.md).

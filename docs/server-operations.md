# Beresta server operations

## First start

The primary deployment is one static executable and one private data directory:

```text
beresta-server --data ./data
```

No configuration file or external database, cache, queue, object store, or
runtime is required. First start creates `beresta.db`, `blobs/`, `backups/`, and
an atomic self-signed `tls/` identity. Compare the logged certificate SHA-256
fingerprint with each client over a trusted channel. Use `--init-only` for a
non-listening initialization check and `verify` for SQLite/blob integrity.
The reviewed household ceiling is five users and three active devices per user.

Global flags must precede the command. Common administration commands are:

```text
beresta-server --data ./data invite --name Alice --ttl 24h
beresta-server --data ./data users list
beresta-server --data ./data device revoke --id <device-id>
beresta-server --data ./data backup --destination <backup-root>
beresta-server --data ./data verify
beresta-server --data ./data verify --backup <backup-directory>
beresta-server --data ./data restore --backup <backup-directory>
beresta-server --data ./data restore --backup <backup-directory> --confirm
beresta-server --data ./data gc
beresta-server --data ./data gc --confirm
```

`invite` prints only the single-use code to stdout; protect shell history and
the channel used to deliver it. Restore and garbage collection are dry runs
unless `--confirm` is supplied. Confirmed restore first creates a separate
pre-restore safety backup, closes SQLite while retaining the exclusive data-root
lock, publishes the verified candidate, and retains rollback paths until the
swap succeeds. Stop the serving process before running offline administration
commands; they fail closed while another process owns the same data directory.

## Backups

At startup and the configured local time, the server creates at most one daily
backup. SQLite is checkpointed and copied with `VACUUM INTO`; immutable complete
blob chunks are hard-linked when possible or copied and rehashed otherwise.
Each published backup has a strict manifest with path, size, SHA-256, and copy
mode. Verification rejects unsafe paths, missing or altered files, and a failed
SQLite `quick_check`. Rotation retains exactly the seven newest valid daily
backups; manual and pre-restore backups do not consume those slots.

Keep at least one backup destination on another physical device. Server backups
protect availability of opaque sync state but cannot decrypt or recover notes
without client-held keys.

## Services and container

The repository includes:

- `build/server/beresta-server.service` for a dedicated Linux `beresta` user;
- `build/server/install-scheduled-task.ps1` for a Windows LocalSystem startup task;
- `build/server/Dockerfile` for an optional scratch container.

Review paths, firewall policy, and the service account before installation.
Direct binary execution remains the recommended deployment. Build all static
targets with `build.cmd server-cross-build`; run the host smoke with
`build.cmd server-smoke`.

```text
docker build -f build/server/Dockerfile -t beresta-server .
docker run --rm -p 8443:8443 -v beresta-data:/data beresta-server
```

The container configuration binds inside its network namespace; preserve the
`/data` volume and verify the generated fingerprint exactly as for a direct
deployment. On Raspberry Pi arm64, run:

```text
sh build/server/measure-idle-pi.sh ./beresta-server
```

The acceptance script verifies first start and health, waits 60 seconds, then
measures process CPU ticks for 10 seconds and requires at most 50 MiB RSS and at
most 1% sampled idle CPU. CI exposes the same check through the opt-in
`beresta-pi` self-hosted runner job.

## Troubleshooting

- A partial `tls/` directory fails closed. Restore both files from a trusted
  backup or remove the incomplete directory only when intentionally creating a
  new identity; clients must then explicitly trust the new fingerprint.
- Configuration parsing is strict. Unknown fields, multiple YAML documents,
  invalid listener addresses, sessions over 24 hours, and altered fixed backup
  retention/chunk sizes prevent startup.
- `429 rate_limited` is transient; clients should use capped backoff. A quota
  failure requires removing unreferenced attachments or changing the reviewed
  per-user quota before retrying.
- WebSocket loss is not data loss. Clients resume cursor polling.

# Beresta server API v1

The home server is an opaque TLS 1.3 transport. It never accepts a passphrase,
Root Key, workspace key, note plaintext, search term, or decrypted attachment.
JSON byte strings use standard base64 encoding. Identifiers are canonical
UUIDv7 values unless a field is documented as a private hexadecimal key/blob
identifier.

## Authentication

`GET /v1/auth/challenge?device_id=<id>&scope=sync` returns a fresh, single-use
challenge. The Ed25519 signature input consists of unsigned 32-bit big-endian
length-prefixed fields in this order:

1. `beresta.auth.v1`
2. challenge ID
3. device ID
4. colon-delimited TLS certificate SHA-256 fingerprint
5. scope (`sync`)
6. nonce

`POST /v1/auth/verify` accepts that signed proof and returns an opaque bearer
token. The server stores only its SHA-256 digest. Sessions expire after at most
24 hours. `POST /v1/auth/refresh` requires both the current bearer session and a
new proof by the same device, then invalidates the old session.

The only public routes are `/health`, `/v1/register`, `/v1/auth/challenge`, and
`/v1/auth/verify`. Every other route requires `Authorization: Bearer <token>`.
Authorization is derived from the authenticated user and current workspace
membership, never a caller-supplied user ID. Forbidden and missing protected
resources both return `404` to avoid an existence oracle.

## Routes

| Method and path | Purpose |
| --- | --- |
| `GET /health` | Database-backed health status; no metrics dependency |
| `POST /v1/register` | Consume one owner-created invite and create the first device/workspace/keybag |
| `GET /v1/auth/challenge` | Issue a device- and server-bound single-use challenge |
| `POST /v1/auth/verify` | Exchange a signed challenge for a scoped session |
| `POST /v1/auth/refresh` | Rotate a session after a new proof by the same device |
| `GET`, `PUT /v1/keybag` | Read or compare-and-swap an opaque versioned keybag |
| `GET`, `POST /v1/workspaces` | List authorized workspaces or create an owned workspace |
| `GET`, `POST /v1/workspaces/{workspace_id}/members` | List or add members; mutation requires the owner |
| `DELETE /v1/workspaces/{workspace_id}/members/{user_id}` | Revoke a non-owner member |
| `GET /v1/workspaces/{workspace_id}/key-envelopes` | Return only the caller's opaque envelopes |
| `PUT /v1/workspaces/{workspace_id}/key-envelopes` | Owner-only atomic current-key rotation covering every active member |
| `GET`, `POST /v1/devices` | List or add devices for the authenticated user |
| `DELETE /v1/devices/{device_id}` | Revoke one of the authenticated user's devices and sessions |
| `POST /v1/sync/ops` | Validate and atomically append a bounded signed operation batch |
| `GET /v1/sync/changes` | Pull ordered changes by workspace cursor and bounded limit |
| `GET /v1/sync/stream` | Authorized WebSocket cursor hints; polling remains authoritative |
| `POST /v1/blobs/init` | Reserve quota and initialize a resumable encrypted blob |
| `PUT`, `GET /v1/blobs/{blob_id}/chunks/{index}` | Upload or download a verified encrypted chunk |
| `GET /v1/blobs/{blob_id}` | Read opaque blob metadata and uploaded-chunk state |
| `POST /v1/blobs/{blob_id}/complete` | Atomically publish a fully verified blob |
| `PUT`, `DELETE /v1/blobs/{blob_id}/references/{reference_id}` | Idempotently add or remove an opaque client reference |
| `POST`, `GET /v1/snapshots` | Upload a signed snapshot or list workspace snapshots |
| `GET /v1/snapshots/latest` | Return the newest compaction-eligible snapshot |
| `GET /v1/snapshots/{snapshot_id}` | Download one authorized opaque snapshot |
| `POST /v1/snapshots/{snapshot_id}/ack` | Store a signed device acknowledgement |

Workspace query parameters are `workspace_id`; change pagination additionally
uses `cursor` and `limit`. Blob chunk uploads use raw request bodies. All other
mutation bodies are strict JSON: unknown fields, trailing values, oversized
bodies, unsupported protocol/schema versions, and invalid canonical IDs fail
closed.

## Errors and notifications

Errors are JSON objects with a stable `code`: `invalid_request`, `unauthorized`,
`not_found`, `conflict`, `quota_exceeded`, `rate_limited`, `timeout`, or
`internal_error`. Keybag compare-and-swap conflicts also return the current
opaque keybag version. WebSocket messages contain only `protocol`,
`workspace_id`, `latest_seq`, and `cursor_epoch`; clients must pull changes and
continue polling if the socket is unavailable.

Signed operation and snapshot field ordering is specified in
[sync-protocol.md](sync-protocol.md). Server defaults and operator commands are
documented in [server-operations.md](server-operations.md).

# Operation schema v1

`beresta.operation.v1` is a closed map with these required keys:

| Key | Type | Rule |
|---|---|---|
| `protocol` | text | Exactly `beresta.sync.v1` |
| `schema_version` | `uint32` | Exactly `1` |
| `op_id` | `bytes(16)` | Valid v1 `op_id` |
| `workspace_id` | `bytes(16)` | Valid v1 `workspace_id` |
| `device_id` | `bytes(16)` | Valid v1 `device_id` |
| `hlc` | map | `beresta.hlc.v1`; its device ID must equal `device_id` |
| `key_id` | `bytes(16)` | Valid v1 `key_id` |
| `nonce` | `bytes(24)` | Random XChaCha20-Poly1305 nonce |
| `ciphertext` | `bytes(16..1048576)` | Encrypted payload including the AEAD tag |
| `sig` | `bytes(64)` | Ed25519 signature |

The client-signed form has no `seq`. The server-stored form adds required `seq: uint64`, greater than zero. `seq` is excluded from the client signature and never changes the immutable signed fields.

After authentication, the decrypted `beresta.operation-payload.v1` closed map contains `payload_version: 1`, a known `mutation_kind`, a valid `object_id`, `crdt_update: bytes|null`, bounded arrays `metadata_updates` and `attachment_refs`, `tombstone: map|null`, and `causal_context: bytes|null`. At least one mutation field must be non-empty. Detailed domain mutations are introduced with the local model and do not change the outer operation schema.

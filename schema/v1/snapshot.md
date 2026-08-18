# Workspace snapshot schema v1

`beresta.workspace-snapshot.v1` is a closed map with required keys:

| Key | Type | Rule |
|---|---|---|
| `protocol` | text | Exactly `beresta.sync.v1` |
| `schema_version` | `uint32` | Exactly `1` |
| `snapshot_id` | `bytes(16)` | Valid v1 `snapshot_id` |
| `workspace_id` | `bytes(16)` | Valid v1 `workspace_id` |
| `base_seq` | `uint64` | Highest contiguous included server sequence |
| `cursor_epoch` | `uint32` | Server cursor epoch |
| `key_id` | `bytes(16)` | Valid v1 `key_id` |
| `creator_device_id` | `bytes(16)` | Valid v1 `device_id` |
| `created_hlc` | map | Valid HLC whose device ID equals `creator_device_id` |
| `nonce` | `bytes(24)` | Random XChaCha20-Poly1305 nonce |
| `ciphertext_hash` | `bytes(32)` | SHA-256 of exact ciphertext bytes |
| `ciphertext` | `bytes(16..268435456)` | Encrypted reconstructible workspace state |
| `sig` | `bytes(64)` | Creator Ed25519 signature |

An acknowledgement is a separate closed signed map containing `protocol`, `schema_version`, `snapshot_id`, `workspace_id`, `device_id`, `base_seq`, `ciphertext_hash`, and `sig`. Acknowledgements for a different hash or sequence are not interchangeable.

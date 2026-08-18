# Encrypted object schema v1

`beresta.encrypted-object.v1` is a closed map:

| Key | Type | Rule |
|---|---|---|
| `schema_version` | `uint32` | Exactly `1` |
| `crypto_profile` | text | Exactly `beresta.crypto.v1` |
| `workspace_id` | `bytes(16)` | Valid v1 `workspace_id` |
| `object_id` | `bytes(16)` | Valid v1 `object_id` |
| `object_type` | text | One of `operation-payload`, `note-snapshot`, `workspace-snapshot`, `revision` |
| `key_id` | `bytes(16)` | Valid v1 `key_id` |
| `nonce` | `bytes(24)` | Random XChaCha20-Poly1305 nonce |
| `ciphertext` | `bytes(16..16777216)` | Ciphertext including tag |

Every field except `nonce` and `ciphertext` is part of the deterministic AAD. The object key derivation also binds `object_id`, `object_type`, and `schema_version`; cross-object substitution must fail authentication.

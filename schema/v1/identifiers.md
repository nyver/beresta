# Identifier schema v1

| Name | Wire type | Validation |
|---|---|---|
| `account_id` | `bytes(16)` | RFC 9562 UUIDv7, variant bits `10` |
| `device_id` | `bytes(16)` | RFC 9562 UUIDv7, variant bits `10` |
| `workspace_id` | `bytes(16)` | RFC 9562 UUIDv7, variant bits `10` |
| `op_id` | `bytes(16)` | RFC 9562 UUIDv7, variant bits `10` |
| `object_id` | `bytes(16)` | RFC 9562 UUIDv7, variant bits `10` |
| `snapshot_id` | `bytes(16)` | RFC 9562 UUIDv7, variant bits `10` |
| `backup_id` | `bytes(16)` | RFC 9562 UUIDv7, variant bits `10` |
| `key_id` | `bytes(16)` | Cryptographically random opaque bytes; all-zero is invalid |
| `blob_id` | `bytes(32)` | HMAC-SHA-256 output; all-zero is invalid |

UUID timestamp bits are informational. Implementations must not infer authorization, causality, or trust from identifier ordering.

# Backup schema v1

`beresta.backup.v1` is a closed file container with a deterministic CBOR header followed by its declared ciphertext bytes. The header map contains:

| Key | Type | Rule |
|---|---|---|
| `magic` | `bytes(8)` | ASCII `BRSTBAK1` |
| `format_version` | `uint32` | Exactly `1` |
| `crypto_profile` | text | Exactly `beresta.crypto.v1` |
| `account_id` | `bytes(16)` | Valid v1 `account_id` |
| `backup_id` | `bytes(16)` | Valid v1 `backup_id` |
| `created_unix_ms` | `uint64` | Creation time for display and retention only |
| `kdf` | map | Same bounded KDF header as keybag v1 |
| `nonce` | `bytes(24)` | Random XChaCha20-Poly1305 nonce |
| `ciphertext_size` | `uint64` | Exact following byte count, at least 16 |

The complete immutable header is AAD. The plaintext is one zstd archive containing a closed manifest, one SQLCipher snapshot, and every referenced encrypted blob. Manifest entries contain a normalized relative UTF-8 path, `size: uint64`, and `sha256: bytes(32)`. Absolute paths, drive prefixes, `..`, empty segments, backslashes, duplicate normalized paths, links, and special files are invalid. Restore authenticates and validates the complete archive in a temporary root before replacing live data.

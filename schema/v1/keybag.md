# Keybag schema v1

`beresta.keybag.v1` is a closed password-encrypted container map:

| Key | Type | Rule |
|---|---|---|
| `magic` | `bytes(8)` | ASCII `BRSTKDF1` |
| `format_version` | `uint32` | Exactly `1` |
| `crypto_profile` | text | Exactly `beresta.crypto.v1` |
| `account_id` | `bytes(16)` | Valid v1 `account_id` |
| `keybag_version` | `uint64` | Greater than zero; compare-and-swap revision |
| `kdf` | map | `beresta.kdf-header.v1` below |
| `nonce` | `bytes(24)` | Random XChaCha20-Poly1305 nonce |
| `ciphertext` | `bytes(16..16777216)` | Encrypted keybag plaintext including tag |

The closed KDF header contains `algorithm: "argon2id"`, `salt: bytes(16)`, `memory_kib: uint32` in `8192..131072`, `time_cost: uint32` in `1..32`, `parallelism: uint32` in `1..64`, and `derived_key_bytes: 32`. Bounds are validated before allocation. Wrong passphrases, corrupt ciphertext, and authentication failures expose the same unlock error.

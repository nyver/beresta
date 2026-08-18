# Attachment manifest schema v1

The server-visible `beresta.attachment-envelope.v1` closed map contains `schema_version: 1`, valid `workspace_id`, `blob_id`, and `key_id`, `manifest_nonce: bytes(24)`, `manifest_ciphertext: bytes(16..1048576)`, `chunk_count: uint32` in `1..512`, `total_ciphertext_bytes: uint64` in `16..2147485696`, and `chunks` as an array of exactly `chunk_count` transport records.

Each transport record is a closed map with `index: uint32`, `ciphertext_size: uint32` in `16..4194320`, and `ciphertext_sha256: bytes(32)`. Indexes are contiguous from zero, sizes sum to `total_ciphertext_bytes` excluding the encrypted manifest, and hashes are diagnostic rather than an authenticity boundary.

After authentication, the encrypted `beresta.attachment-manifest.v1` closed map contains:

- `manifest_version: 1`;
- `plaintext_size: uint64` in `0..2147483648`;
- `media_type: text(1..255)`;
- `display_name: text(1..1024)` containing no path separators or control characters;
- `chunk_count: uint32` in `1..512`;
- `chunks`, with exactly one record per index containing `index`, `plaintext_size: uint32` in `0..4194304`, `nonce: bytes(24)`, `ciphertext_size`, and `ciphertext_sha256`.

The last chunk may have zero plaintext bytes only for an empty attachment. All other chunks are non-empty. Complete plaintext size and workspace-private `blob_id` are recomputed before publication.

# Beresta Cryptographic Profile

## Status

This document defines `crypto_profile_v1`. Implementations must match it byte for byte and pass the compatibility vectors under `schema/testdata`. Changes that alter key derivation, encoding, authenticated data, signatures, or ciphertext compatibility require a new profile identifier and an ADR.

Beresta uses established primitives from the Go standard library and `golang.org/x/crypto`. It does not define a new cryptographic primitive.

## Profile Identifier and Encoding Rules

The profile identifier is the UTF-8 string `beresta.crypto.v1`.

Cryptographic structures use deterministic CBOR as defined by `schema/formats.md`. Values used as AEAD associated data, signature inputs, HKDF context, or HMAC input must be encoded by the canonical encoder; accepting a semantically equivalent non-canonical encoding does not make that encoding valid signed material.

All integers are unsigned unless a schema explicitly says otherwise. Byte strings remain byte strings and are never converted through locale-dependent text. Text is UTF-8 and normalized only where the schema explicitly requires it. UUIDs are encoded as their 16 raw bytes in cryptographic inputs.

Lengths in concatenated HKDF contexts use a four-byte unsigned big-endian prefix. Literal domain strings below are ASCII bytes without a trailing NUL.

## Primitive Suite

| Purpose | Primitive |
|---|---|
| Password derivation | Argon2id |
| Domain-separated key derivation | HKDF-SHA-256 |
| Content, keybag, backup, and chunk encryption | XChaCha20-Poly1305 |
| User identity encryption key | X25519 |
| Anonymous workspace-key envelope | `golang.org/x/crypto/nacl/box.SealAnonymous` compatible sealed box |
| Account and device signatures | Ed25519 |
| Private attachment identity | HMAC-SHA-256 |
| Manifest and diagnostic integrity hash | SHA-256 |
| Randomness | Operating-system CSPRNG through `crypto/rand` |

XChaCha20-Poly1305 keys are 32 bytes and nonces are 24 bytes. Ed25519 and X25519 key sizes follow their standard encodings. Authentication failures return one stable error category and never expose partially decrypted plaintext.

## Password Derivation

### Stored KDF header

Every password-encrypted keybag and standalone backup records this authenticated cleartext header:

```text
magic              = "BRSTKDF1" or "BRSTBAK1"
format_version     = 1
crypto_profile     = "beresta.crypto.v1"
kdf                = "argon2id"
salt               = 16 random bytes
memory_kib         = calibrated unsigned integer, maximum 131072
time_cost          = calibrated unsigned integer, minimum 1
parallelism        = calibrated unsigned integer, minimum 1
derived_key_bytes  = 32
```

Decoders reject zero values, memory above 128 MiB, unsupported algorithms, unsupported profile versions, integer overflow, and parameters that exceed platform safety limits before allocating memory.

### Calibration

At account creation, a device starts from `memory_kib=131072`, `time_cost=3`, and `parallelism=min(4, useful logical CPUs)`. It lowers memory when the platform cannot safely reserve 128 MiB, then adjusts time cost toward one second on that device. The chosen values are stored with the ciphertext and are authoritative for later derivation.

Calibration must run through the same bounded implementation used for unlock, must not exceed 128 MiB, and must be cancellable before a derivation begins. A device may warn about parameters that are unusually weak for its capability but must use the authenticated stored parameters to unlock existing data.

### Root Key

```text
RootKey = Argon2id(
    password = UTF8(passphrase),
    salt = header.salt,
    time = header.time_cost,
    memoryKiB = header.memory_kib,
    threads = header.parallelism,
    keyLen = 32,
)
```

The passphrase and Root Key never leave the client. Passphrases are not normalized or silently trimmed; UI confirmation prevents accidental leading/trailing whitespace. Implementations must not convert the Root Key to a string or log KDF inputs.

## HKDF Domain Separation

The generic derivation is:

```text
Derive(ikm, domain, parts..., length=32) =
    HKDF-SHA-256(
        ikm = ikm,
        salt = nil,
        info = LP("beresta.crypto.v1") || LP(domain) || LP(part[0]) || ...,
        length = length,
    )
```

`LP(x)` is the four-byte big-endian byte length followed by `x`.

Required domains are:

| Domain | Input key | Context parts | Output |
|---|---|---|---|
| `keybag` | Root Key | account ID | Keybag Key |
| `workspace-object` | Workspace Key | object ID, object type, schema version | Object Key |
| `blob-id` | Workspace Key | workspace ID | Blob ID Key |
| `blob-manifest` | Workspace Key | blob ID | Blob Manifest Key |
| `blob-chunk` | Workspace Key | blob ID, chunk index | Chunk Key |
| `backup` | Root Key | account ID, backup ID | Backup Key |
| `pairing-export` | SPAKE2 session key | session ID, transcript hash | Pairing Export Key |

New purposes must use a new domain string. Reusing a domain with a different semantic input is prohibited.

## Key Hierarchy and Storage

```text
Passphrase
└── Argon2id(header) → Root Key
    ├── HKDF("keybag", account_id) → Keybag Key
    └── HKDF("backup", account_id, backup_id) → Backup Key

Keybag plaintext
├── Account X25519 identity key pair
├── Account Ed25519 authority key pair
├── Workspace key records
│   ├── workspace_id
│   ├── key_id
│   ├── 32-byte Workspace Key
│   └── activation and retirement metadata
└── Public device registry metadata

Per-device protected state
├── Random SQLCipher database key, wrapped by OS keystore
└── Device Ed25519 signing key, wrapped by OS keystore
```

The account authority signing key signs membership, device authorization, revocation, and key-transition records. Every device has a distinct Ed25519 key for server challenge authentication and operation signatures. Device private keys are not synchronized in the keybag; a newly paired or passphrase-bootstrapped device generates its own key and receives an account-authorized device record.

This separation is required so one device can be revoked without invalidating every other device. The server stores account/device public keys and opaque encrypted keybags, never private key material.

## Platform Key Wrapping

Local device keys use the canonical `BKW1` envelope. The header contains magic `BKW1`, format version, protection code, length-delimited key ID and purpose, and the platform ciphertext length. The OS operation receives this public binding:

```text
UTF8("beresta-keystore-v1") ||
protection_code ||
U16BE(len(key_id)) || UTF8(key_id) ||
U16BE(len(purpose)) || UTF8(purpose)
```

Key IDs and purposes are bounded ASCII tokens. A protection, key-ID, purpose, version, length, or trailing-data substitution fails before OS unwrapping. Platform ciphertext is bounded to 64 KiB, and returned plaintext enters an owned mutable secret immediately.

- Windows DPAPI uses current-user scope, forbids DPAPI-owned UI, and supplies the binding as optional entropy. Windows 10 uses this explicit fallback mode.
- Windows 11 build 22000 and later selects Hello mode when `UserConsentVerifier` is available. The owner-window WinRT prompt must verify the user before DPAPI unwrap; a Hello envelope cannot be opened through the application's fallback adapter.
- Android uses a non-exportable 256-bit AES-GCM key in `AndroidKeyStore`. The binding is AEAD associated data. Biometric mode configures authentication for every use, passes the initialized cipher through `BiometricPrompt.CryptoObject`, permits only strong biometrics, and invalidates the key after biometric enrollment changes.

Windows Hello in this profile is an application user-presence gate over current-user DPAPI, not a separate hardware-bound wrapping key. Code already executing as the same Windows user can invoke DPAPI outside Beresta and is covered by the endpoint-compromise limitation in the threat model. The UI must display the active protection mode and must not describe Hello-gated DPAPI as protection from same-user malware.

## Keybag Encryption

The keybag container consists of:

```text
header          # KDF header plus account_id and keybag_version
nonce           # 24 random bytes
ciphertext      # XChaCha20-Poly1305 output including tag
```

```text
KeybagKey = Derive(RootKey, "keybag", account_id)
AAD = CanonicalCBOR({
    "container": "keybag",
    "format_version": 1,
    "crypto_profile": "beresta.crypto.v1",
    "account_id": account_id,
    "keybag_version": keybag_version,
    "kdf": complete_kdf_header,
})
ciphertext = XChaCha20Poly1305.Seal(nonce, keybag_plaintext, AAD)
```

The server compare-and-swap version is the same authenticated `keybag_version`. A stale server response cannot be substituted under a newer version without authentication failure.

## Object Encryption

Each encrypted object has a visible envelope:

```text
schema_version
crypto_profile
workspace_id
object_id
object_type
key_id
nonce
ciphertext
```

The Object Key and AAD are:

```text
ObjectKey = Derive(
    WorkspaceKey,
    "workspace-object",
    object_id,
    UTF8(object_type),
    U32BE(schema_version),
)

AAD = CanonicalCBOR({
    "schema_version": schema_version,
    "crypto_profile": "beresta.crypto.v1",
    "workspace_id": workspace_id,
    "object_id": object_id,
    "object_type": object_type,
    "key_id": key_id,
})

ciphertext = XChaCha20Poly1305.Seal(random_nonce, plaintext, AAD)
```

`object_type` is a closed schema value such as `operation-payload`, `note-snapshot`, `workspace-snapshot`, or `revision`. A ciphertext cannot be moved between objects, workspaces, types, schema versions, or key IDs without failing authentication.

## Operation Signatures

The client signs the envelope after encrypting the operation payload. The server-assigned sequence is not present when the client signs and is therefore excluded. The signature input is:

```text
signature_input =
    LP("beresta.operation.signature.v1") ||
    CanonicalCBOR({
        "op_id": op_id,
        "workspace_id": workspace_id,
        "device_id": device_id,
        "hlc": hlc,
        "key_id": key_id,
        "nonce": nonce,
        "ciphertext": ciphertext,
    })

sig = Ed25519.Sign(device_private_key, signature_input)
```

Clients and the server reject non-canonical input, unknown mandatory fields, invalid key sizes, invalid signatures, revoked devices, unacceptable HLC windows, and duplicate `op_id` values before applying or sequencing an operation.

Snapshot, snapshot-acknowledgement, membership, device authorization,
revocation, and key-transition records use distinct signature domain strings
and their own canonical schemas. Snapshot records use
`beresta.snapshot.signature.v1`; acknowledgements use
`beresta.snapshot-ack.signature.v1`. A signature valid for one record class is
invalid for every other class.

## Workspace-Key Envelopes

Sharing encrypts the current 32-byte Workspace Key to the recipient's account X25519 public key:

```text
envelope_plaintext = CanonicalCBOR({
    "crypto_profile": "beresta.crypto.v1",
    "workspace_id": workspace_id,
    "key_id": key_id,
    "workspace_key": workspace_key,
    "sender_account_id": sender_account_id,
    "recipient_account_id": recipient_account_id,
})

sealed = box.SealAnonymous(recipient_x25519_public_key, envelope_plaintext, crypto/rand)
```

The account authority signs the visible envelope metadata and sealed-box hash. The recipient verifies that signature and all IDs after opening the envelope. The server stores only membership metadata, the sealed bytes, and signatures.

## Attachment Identity and Encryption

### Private identifier

```text
BlobIDKey = Derive(WorkspaceKey, "blob-id", workspace_id)
blob_id = HMAC-SHA-256(BlobIDKey, plaintext_attachment_bytes)
```

The HMAC is streamed over the complete plaintext. It permits deduplication only within the same workspace key domain and prevents a global known-file hash oracle.

### Chunks

Attachments are divided into chunks of at most 4 MiB. For chunk index `i`:

```text
ChunkKey = Derive(WorkspaceKey, "blob-chunk", blob_id, U64BE(i))
ChunkAAD = CanonicalCBOR({
    "crypto_profile": "beresta.crypto.v1",
    "workspace_id": workspace_id,
    "blob_id": blob_id,
    "key_id": key_id,
    "chunk_index": i,
    "plaintext_size": chunk_plaintext_size,
})
encrypted_chunk = XChaCha20Poly1305.Seal(random_nonce_i, chunk_plaintext, ChunkAAD)
```

The encrypted manifest contains original size, media type, safe display name, chunk count, per-chunk nonce, ciphertext size, and SHA-256 of each encrypted chunk for transport diagnostics. The manifest itself is encrypted with `BlobManifestKey` and authenticated to workspace, blob ID, and key ID. SHA-256 fields are not plaintext identities and are never used instead of AEAD verification.

Clients make an attachment visible only after manifest authentication, all chunk AEAD checks, complete plaintext size validation, and recomputation of the private blob ID.

## Backup Encryption

A standalone client backup header includes the password KDF header, account ID, backup ID, creation time, format version, and crypto profile. After deriving the Root Key from the user passphrase:

```text
BackupKey = Derive(RootKey, "backup", account_id, backup_id)
BackupAAD = CanonicalCBOR(complete immutable backup header)
ciphertext = XChaCha20Poly1305.Seal(random_nonce, zstd_archive, BackupAAD)
```

The encrypted archive contains a SHA-256 manifest for the SQLCipher snapshot and every included encrypted blob. SHA-256 detects accidental corruption during catalog and copy operations; XChaCha20-Poly1305 remains the authenticity boundary.

Pre-restore validation derives keys and authenticates in a temporary location. No archive-provided path is written outside the temporary restore root.

## Nonce Policy

- Every XChaCha20-Poly1305 encryption receives a fresh 24-byte nonce directly from `crypto/rand.Reader`.
- Nonces are stored beside ciphertext and are not secret.
- Counter-, timestamp-, UUID-, HLC-, hash-, or deterministic nonces are prohibited in profile v1.
- APIs generate nonces internally; callers cannot supply production nonces.
- Test-only deterministic randomness is injected through an unexported or explicitly test-scoped dependency and cannot be selected by production configuration.
- A CSPRNG read that returns an error or short result aborts encryption without emitting an envelope.
- Copying ciphertext under a new random nonce without decryption and re-encryption is invalid.

The 192-bit nonce space makes accidental collision negligible when nonces are generated correctly. Implementations still include duplicate-nonce property tests around mocked randomness and refuse to continue when the injected source repeats values during a test operation.

## Key Rotation and Revocation

Each workspace key record has an opaque random `key_id`, activation HLC, and state (`current`, `historical`, or `retired`). New writes use exactly one current key.

On member or device revocation:

1. The account authority signs a revocation record with an explicit server enforcement boundary.
2. The client generates a new random Workspace Key and key ID.
3. It seals the new key to every remaining recipient and signs the envelope set.
4. It publishes an encrypted key-transition operation.
5. New content uses the new key immediately; synchronization does not pause for bulk re-encryption.
6. Authorized clients retain historical keys to read old content.
7. An optional resumable hardening job re-encrypts live objects and snapshots under the new key and publishes ordinary idempotent operations.

Rotation cannot erase data or keys already copied by a previously authorized device. Retiring a key only removes it after no retained object, revision, backup policy, or supported device requires it.

## Key Lifetime and Memory Handling

- Passphrase buffers, Root Keys, workspace keys, backup keys, database keys, and private signing keys use owned mutable byte buffers.
- APIs avoid string conversion, interface boxing, reflection-based logging, and unnecessary serialization of secrets.
- Buffers are wiped on lock, close, cancellation, error cleanup, and process lifecycle hooks where execution is possible.
- Long-lived device and database keys remain wrapped by the OS keystore while the account is locked.
- Decrypted keybags are parsed into bounded structures and their source buffer is wiped after owned key records are established.
- Errors contain stable categories and opaque identifiers, never key bytes, passphrases, plaintext, nonces plus plaintext, or decrypted structure dumps.

Go cannot guarantee erasure of compiler/runtime copies, stacks, registers, or library-internal buffers. The threat model records this as a residual limitation; explicit zeroization is defense in depth, not a claim of perfect forensic erasure.

## Test Vector Format

Compatibility vectors are JSON documents for reviewability, while encoded values are lowercase hexadecimal strings. A vector file includes:

```json
{
  "profile": "beresta.crypto.v1",
  "case": "object-encryption-001",
  "inputs": {
    "workspace_key_hex": "...",
    "object_id_hex": "...",
    "nonce_hex": "...",
    "plaintext_hex": "..."
  },
  "expected": {
    "derived_key_hex": "...",
    "aad_cbor_hex": "...",
    "ciphertext_hex": "..."
  }
}
```

Required vector groups are:

- Argon2id Root Key derivation with fixed parameters and UTF-8 passphrases.
- Every HKDF domain and length-prefix boundary.
- Keybag AAD and ciphertext.
- Object key, AAD, encryption, decryption, and cross-object rejection.
- Ed25519 operation, snapshot, membership, revocation, and transition signatures.
- X25519 anonymous sealed-box interoperability.
- Blob HMAC identity, manifest, first/middle/final chunks, empty file, and exact 4 MiB boundary.
- Backup header, derived key, authenticated encryption, and corruption rejection.
- Platform key-wrapping envelope/binding bytes and protection-substitution rejection.
- Canonical CBOR rejection for duplicate keys, alternate integer widths, invalid UTF-8, excessive nesting, unknown required fields, and trailing bytes.

Vector randomness is fixed and public and must never be copied into production fixtures as a secret. Each implementation must both consume checked-in expected vectors and generate equivalent outputs for independent comparison.

## Error Handling

The public crypto layer exposes stable categories: unsupported profile, invalid parameters, malformed envelope, authentication failed, signature failed, revoked key, random source failed, and resource limit exceeded. Wrong passwords, corrupt keybags, and keybag authentication failures share the same externally visible unlock error.

Decryption authenticates before returning plaintext. Callers receive no partially decoded object on error. Files and temporary buffers created before a failure are removed or retained only as explicitly quarantined ciphertext with no secret-bearing filename.

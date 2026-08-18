# Beresta Threat Model

## Status and Scope

This document describes security threats and required mitigations for Beresta clients, the optional home synchronization server, backups, transports, packaging, and diagnostics. It uses STRIDE as an organizing method and records limitations that the product must disclose.

The model assumes a household deployment of up to five users. Small scale reduces operational complexity but does not weaken user isolation, authorization, cryptographic verification, or recovery requirements.

## Security Objectives

- Note, attachment, revision, search-index, and backup plaintext is available only on an authorized unlocked client.
- Passwords, Root Keys, unwrapped workspace keys, and local database keys never reach the server or application logs.
- A copied server data directory and captured application traffic do not reveal user content.
- Remote operations are accepted only when authenticated, signed by a current device, authorized for the workspace, and not replayed.
- Offline edits converge without silent loss, duplication, or partial application.
- A stolen locked client does not reveal its encrypted database or attachment content without both platform-keystore access and user authentication.
- Backups can be verified and restored without trusting the synchronization server.
- Revocation blocks future server authentication and future-key content while accurately disclosing that previously downloaded data cannot be erased remotely.

## Assets

### Highest sensitivity

- User passphrases and password-derived Root Keys.
- X25519 identity private keys and Ed25519 device signing private keys.
- Current and historical workspace keys.
- Per-device SQLCipher keys and backup keys.
- Decrypted note bodies, metadata, attachments, revisions, and search indexes.
- Decrypted content rendered in application windows, clipboard data, previews, and temporary exports.

### Integrity and availability critical

- Encrypted keybags and their KDF/profile metadata.
- Signed encrypted operation logs, cursors, HLC state, tombstones, and snapshots.
- Memberships, device status, revocation boundaries, and workspace-key envelopes.
- Client and server databases, encrypted blob stores, and backup manifests.
- Pinned TLS fingerprints, update-signing keys, release signatures, and CI provenance.

## Adversaries and Assumptions

The model considers:

- A thief with a powered-off or locked client device and its storage.
- A thief or administrator with the complete server data directory.
- A malicious or compromised synchronization server that can observe, delay, duplicate, reorder, omit, or replace stored data.
- A network attacker capable of DNS, routing, proxy, and TLS interception attempts.
- Another authenticated Beresta user attempting cross-user or cross-workspace access.
- A revoked device or member retaining credentials and previously downloaded ciphertext/plaintext.
- Malicious imported archives, shared files, filenames, operation envelopes, and protocol payloads.
- Malware or an attacker controlling an unlocked client process.
- A compromised build dependency, update channel, or release artifact.

The operating-system kernel, platform keystore implementation, CSPRNG, and cryptographic library implementations are trusted within their documented guarantees. A fully compromised unlocked client is outside the confidentiality boundary: it can observe plaintext and live keys available to that process. Beresta still limits persistence, logging, and subsequent access where practical.

## Trust Boundaries

1. **Locked device to unlocked client:** platform authentication releases a wrapped local database key and permits local keybag decryption.
2. **UI to Go core:** untrusted presentation input becomes validated commands; the UI cannot issue SQL or create signed operations.
3. **Go core to local storage:** plaintext exists in process memory and the encrypted SQLCipher connection; persistent files must remain encrypted.
4. **Client to network:** TLS 1.3 protects transport while independently encrypted, authenticated, signed payloads protect application content.
5. **Network to optional server:** every request crosses authentication, authorization, limits, validation, and transaction boundaries.
6. **Server database to blob filesystem:** metadata references immutable encrypted chunks; publication must be atomic across the boundary.
7. **Application to backup/export destinations:** encrypted backup is the default; plaintext export requires a separate explicit warning and confirmation.
8. **Build system to released artifact:** dependencies, generated bindings, signatures, checksums, and provenance must remain verifiable.

## STRIDE Analysis

### Spoofing

| Threat | Impact | Required mitigation | Verification |
|---|---|---|---|
| Attacker impersonates a registered device | Unauthorized sync, data injection, metadata access | Fresh single-use server challenge signed by the device Ed25519 key; scoped hashed sessions with at most 24-hour TTL; refresh requires new proof | Challenge replay, wrong-key, expired-session, and revoked-device tests |
| Attacker impersonates the home server | Credential capture, traffic manipulation, denial of sync | TLS 1.3 and certificate fingerprint pinning learned from a trusted connection QR; explicit trust-reset flow; public PKI allowed only when configured | MITM proxy and certificate-substitution tests |
| Attacker guesses or reuses an invite | Unauthorized account creation | High-entropy, named, expiring, single-use invite codes stored hashed; no open-registration endpoint | Invite entropy, expiration, race, and reuse tests |
| Nearby attacker hijacks device pairing | Keybag disclosure or malicious device enrollment | SPAKE2-authenticated local channel, QR session material, short authentication string confirmed on both devices, strict expiry and single use | Mismatched-code, replay, relay, timeout, and cancellation tests |
| Malicious update claims to be Beresta | Code execution and secret theft | Signed installers and updates, publisher verification, artifact checksums and provenance, rollback on verification failure | Tampered-update release tests |

### Tampering

| Threat | Impact | Required mitigation | Verification |
|---|---|---|---|
| Server changes encrypted note or blob bytes | Corruption or attacker-chosen plaintext attempt | XChaCha20-Poly1305 authentication with canonical AAD binding profile, schema, workspace, object, type, key, and chunk | Bit flips and cross-object substitution vectors |
| Server changes visible operation routing metadata | Cross-workspace injection, forged causality | Ed25519 signature covers canonical visible immutable metadata, nonce, and ciphertext; client revalidates membership and key ID | Metadata substitution and non-canonical CBOR tests |
| Server reorders, duplicates, or omits operations | Stale state, repeated effects, inconsistent replicas | Durable cursor, `op_id` idempotency, CRDT convergence, HLC validation, gap detection, snapshot/state-vector checks | Random permutation/duplication/drop and recovery property tests |
| Local crash leaves partial database or blob state | Corruption, missing attachment, duplicate operation | SQLite transactions/WAL; immutable temp-write, flush, rename publication; database reference committed last; grace-period orphan collection | Injected termination at every durability boundary |
| Backup archive is modified | Corrupt or attacker-controlled restore | Authenticated encryption plus SHA-256 manifest, full pre-restore validation in temporary storage, atomic swap | Corrupt manifest/chunk and interrupted-restore tests |
| Imported archive uses path traversal or malformed content | File overwrite, memory exhaustion, parser exploit | Normalize and confine paths, reject absolute/parent traversal, bounded streaming parse, size/count/depth limits, safe filenames | Malicious `.enex` and portable-archive corpus |

### Repudiation

| Threat | Impact | Required mitigation | Verification |
|---|---|---|---|
| Device denies submitting an operation | Disputed workspace mutation | Device Ed25519 signature, durable operation ID, device ID, HLC, and server acceptance sequence | Signature and audit-record tests |
| Operator denies revocation or membership change | Ambiguous access boundary | Signed administrative records with explicit effective boundary and durable server transaction | Revocation race and ordering tests |
| Restore or export occurs without user awareness | Unexpected data replacement or plaintext exposure | Explicit confirmation, pre-restore safety snapshot, immutable local audit metadata without content, export warning | UI and application-service tests |

Beresta does not provide legal-grade non-repudiation. Local users control their devices and may erase local logs. Audit data exists to diagnose synchronization and authorization, not to establish identity to a third party.

### Information Disclosure

| Threat | Impact | Required mitigation | Verification |
|---|---|---|---|
| Server disk is stolen | Exposure of all synchronized content | Server stores only opaque keybags, envelopes, operations, snapshots, and encrypted blobs; no password or content-derived unkeyed identifiers | Offline inspection of complete server data root |
| Locked client disk is stolen | Exposure of notes, FTS index, attachments, or keys | SQLCipher database, encrypted blob files, random per-device database key wrapped by OS keystore, encrypted keybag | Stolen-directory and memory-absence tests |
| Wrapped-key metadata or protection mode is substituted | Cross-key unwrap or authentication downgrade | Canonical `BKW1` envelope; key ID, purpose, version, and protection bound as DPAPI entropy or Android AES-GCM AAD; adapters reject cross-mode envelopes | Golden-vector, tamper, and downgrade tests |
| Android biometric enrollment changes after setup | A newly enrolled biometric unlocks an old wrapping key | Authentication-per-use strong-biometric key, `BiometricPrompt.CryptoObject`, and permanent key invalidation on enrollment change | Physical-device invalidation test |
| Traffic is intercepted | Password/key/content disclosure | TLS 1.3 plus independent payload encryption; device-key auth sends signatures, never passwords | MITM capture with seeded secrets and plaintext corpus |
| Logs or crash reports contain secrets | Persistent accidental disclosure | Allow-listed structured fields; no arbitrary DTO/object logging; content-free errors; seeded-secret scanner in CI | Log/crash corpus scan |
| Attachment hash reveals known plaintext | Cross-user equality oracle | Per-workspace `HMAC(BlobIDKey, plaintext)` identifiers, encrypted independently authenticated chunks | Equal-file tests across independent workspaces |
| Android recent-apps overview or screenshot captures notes | Shoulder surfing or OS snapshot disclosure | Secure-screen flags, redacted recent-apps surface, lock on configured lifecycle boundary | Android UI automation and manual release test |
| Clipboard, temp file, or plaintext export persists | Local plaintext residue | Avoid clipboard use for keys; bounded clipboard content policy; safe temp directory and cleanup; explicit export location and warning | Termination and cleanup tests |
| Backup destination is copied | Offline disclosure | Backups encrypted under a derived backup key and self-contained encrypted blob set | Backup-directory inspection without keybag/passphrase |

### Denial of Service

| Threat | Impact | Required mitigation | Verification |
|---|---|---|---|
| Oversized request, operation, CBOR value, or blob | Memory/disk exhaustion | Body, field, nesting, operation, chunk, blob, and quota limits enforced before allocation/commit | Boundary and over-limit tests |
| Authentication or invite brute force | CPU or resource exhaustion | Bounded in-memory rate limits, short challenge expiry, constant-shape failures, request deadlines | Load and rate-limit tests |
| Malicious HLC far in the future | LWW starvation and corrupted ordering | Configured clock-skew window, reject/quarantine invalid HLC, persist monotonic local clock | Future/past skew tests |
| WebSocket subscriber is slow | Goroutine or memory leak | Bounded channels, cursor hints rather than operation bodies, drop/disconnect policy, polling fallback | Slow-consumer and reconnect tests |
| Blob upload remains incomplete | Disk exhaustion | Quota reservation, staged-upload expiry, atomic completion, garbage collection | Abandoned-upload recovery tests |
| Corrupt operation blocks cursor forever | Permanent sync outage | Quarantine with explicit user-visible diagnostics and safe retry/recovery workflow; never silently skip authenticated history | Poison-operation end-to-end test |
| Argon2 parameters exhaust mobile memory | Crash during unlock | Device calibration toward one second with a hard 128 MiB ceiling and bounded parallelism; validate stored parameters before allocation | Malicious-profile and low-memory-device tests |
| Backup rotation consumes all storage | Client outage or history loss | Preflight capacity, external destinations, content-addressed reuse where safe, warn without deleting valid history | Low-space rotation tests |

### Elevation of Privilege

| Threat | Impact | Required mitigation | Verification |
|---|---|---|---|
| Authenticated user substitutes another resource ID | Cross-user data access or mutation | Load resources through authenticated ownership/membership scope; deny by default on every handler | Generated IDOR matrix for all resources/actions |
| Revoked device continues to authenticate or submit ops | Future unauthorized access | Immediate session invalidation and signed-operation rejection at revocation boundary; rotate workspace keys for future content | Concurrent revoke/auth/push tests |
| Client UI invokes internal storage or key operations | Bypass validation and policy | Coarse Wails/mobile application APIs; no raw SQL, filesystem, nonce, or key methods exposed | Binding-surface review and tests |
| Update process replaces arbitrary files or runs unsigned code | Local privilege/code execution | Fixed install roots, signature/publisher verification before replacement, least privilege, rollback to known-good version | Installer/update tampering tests |
| Server path or blob ID escapes data root | Arbitrary file read/write | Strict canonical encoded identifiers, fixed content-addressed layout, safe joins, no user path segments | Traversal and alternate-separator tests |

## Device Theft and Compromise

### Locked or powered-off device

An attacker obtains SQLCipher files, encrypted blobs, wrapped database key material, and encrypted keybags. Access requires defeating platform keystore policy and user authentication. The application must not store passphrases, Root Keys, decrypted FTS indexes, or plaintext attachment caches on disk.

On Windows 11+, Beresta requests Windows Hello in the owner window before its DPAPI unwrap. This improves unattended and shoulder-surfing resistance inside the application but does not make user-scoped DPAPI ciphertext inaccessible to malware already executing as that Windows user. Windows 10 uses an explicitly labeled DPAPI-only fallback. Android biometric mode cryptographically authorizes each AES-GCM operation through Android Keystore and fails closed when the biometric key is unavailable or invalidated.

### Unlocked device or process compromise

An attacker controlling the unlocked process can read content available to that process and may copy live keys. Beresta reduces exposure by locking on inactivity/background policy, keeping private material wrapped while locked, minimizing copies, zeroing owned buffers, and redacting diagnostics. These mitigations cannot make an unlocked compromised endpoint trustworthy.

### Revoked device

Revocation prevents new server sessions and operations, rotates keys for future content, and can trigger local self-wipe after authenticated proof. It cannot erase plaintext, ciphertext, screenshots, exports, backups, or historical keys already copied by the device. The confirmation UI and this threat model must state that limitation directly.

## Malicious or Compromised Server

The server can observe user/device/workspace routing identifiers, membership, key IDs, sequence numbers, object sizes, timing, ciphertext, and signatures. It can perform traffic analysis and can deny, delay, reorder, duplicate, or delete data. E2EE does not hide these access patterns.

Clients detect ciphertext and signature tampering, duplicate operation IDs, unacceptable HLC values, cursor gaps where representable, and invalid snapshot transitions. A malicious server can still cause denial of service or withhold the newest data. Independent client backups and optional folder/LAN exchange provide recovery paths; Beresta does not claim availability against a malicious sole transport.

Snapshot compaction never trusts opaque snapshot bytes from one device as sufficient proof for destructive deletion. Every active authorized device acknowledges the same signed snapshot, or is explicitly revoked and aged past the retention boundary, before earlier operations become collectible.

## Backup and Export Threats

Encrypted backups inherit the confidentiality of the user's passphrase-derived backup key. Manifests authenticate profile, timestamps, database hash, note count, and referenced blobs. Restore opens candidates read-only in a temporary location, validates all content and migrations, and creates a safety backup before replacement.

Plaintext export intentionally crosses the encryption boundary. The UI must identify the destination as unencrypted, require explicit confirmation, stage writes safely, clean partial output, and recommend an encrypted destination. Beresta cannot control copies created by backup software, cloud providers, removable media, or the user after export.

## Logging, Metrics, and Crash Handling

Logs use an allow list: request ID, coarse action, status, duration, bounded sizes, opaque resource IDs where necessary, and stable error codes. They must not serialize request/response bodies, operation plaintext, ciphertext excerpts, HTTP authorization values, invite codes, key material, filenames containing user content, or stack-local buffers.

Metrics are aggregate and optional. Labels must not contain user, workspace, note, blob, device, or request identifiers. Crash handlers redact environment variables, arguments, clipboard, request bodies, and application memory attachments. CI seeds recognizable secrets and fails if any appear in captured logs or crash metadata.

## Cryptographic and Runtime Residual Risks

- Go is garbage collected. Explicitly wiping an owned byte slice cannot prove that compiler/runtime copies, register values, stack growth copies, or library internals were erased. APIs therefore minimize copies and string conversion, but forensic zeroization is best effort.
- Key rotation protects future content. Previously authorized recipients may retain historical keys and downloaded data.
- Workspace membership, device identifiers, object sizes, timing, and access frequency remain visible to the server.
- Self-signed certificate pinning relies on a trustworthy first QR transfer. A user who accepts a fingerprint through an attacked channel can pin the attacker.
- Availability is not guaranteed against deletion by a malicious server, client ransomware, loss of all devices and backups, or catastrophic passphrase loss.
- SQLCipher and platform keystores reduce offline risk but cannot protect a device with a compromised kernel, accessibility service, debugger, or unlocked administrative session.
- Windows Hello gates Beresta's DPAPI unwrap path but cannot prevent same-user code from invoking DPAPI directly; it is not claimed as a defense against an already compromised Windows session.

## Security Verification Gates

Every release must pass:

- Cryptographic golden vectors, malformed-input fuzzing, and nonce/AAD misuse tests.
- CRDT convergence and randomized operation-order property tests.
- Complete server IDOR and revoked-device authorization matrices.
- TLS pinning and intercepted-traffic assertions for seeded plaintext and keys.
- Stolen client/server/backup directory inspection tests.
- Log and crash-artifact secret scans.
- Backup destroy/restore hash verification.
- Forced-termination recovery across local mutation, migration, backup, synchronization, compaction, and blob publication.
- `govulncheck`, OSV dependency scanning, signed artifact verification, and release provenance checks.

Security exceptions require an ADR describing the affected objective, exploitability, compensating controls, test coverage, user disclosure, migration, and removal plan. Fixed E2EE and local-only requirements cannot be waived as a temporary implementation shortcut.

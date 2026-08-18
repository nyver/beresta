# Beresta Assumptions

This file records implementation decisions that fill gaps without changing the fixed product and security requirements. A decision moves to an ADR when it changes architecture, compatibility, security boundaries, or migration behavior.

## Product and Data Model

1. A local account can exist without any server identity. Optional server enrollment adds transport credentials to the same account and does not migrate note data.
2. A workspace is the cryptographic sharing boundary. The initial personal workspace can contain a notebook tree; additional shared workspaces remain independently keyed.
3. Note bodies are rich-text CRDT documents with a canonical Markdown projection. Markdown is used for search/export/diff presentation but is not the merge source.
4. Tags use per-tag metadata registers so concurrent changes to unrelated tags do not replace the entire tag set.
5. Restore creates new current operations rather than rewinding synchronization history.
6. Revision rollback and portable/Evernote import recreate a note's content as plain text. Neither the canonical Markdown export nor Evernote's ENML has a parser back into the CRDT rich-text model in this codebase, so formatting is not round-tripped through either path; both report this to the user (rollback implicitly, import as a per-note warning) rather than silently dropping it.

## Identity and Keys

1. The account has an X25519 identity key and an Ed25519 authority key in the encrypted keybag.
2. Every device generates a distinct Ed25519 signing key wrapped by its OS keystore. Device private keys are not synchronized; this is necessary for independent revocation.
3. The account authority key signs device authorization, membership, revocation, and workspace-key transition records. Device keys sign authentication challenges, ordinary operations, snapshots, and acknowledgements.
4. Workspace-key rotation changes the key for new content immediately. Historical keys remain available to authorized clients until retention and re-encryption rules permit retirement.
5. A user who loses every passphrase/keybag-capable device and every valid encrypted backup cannot recover the account through the server.
6. Windows 11 build 22000 and later uses owner-window `UserConsentVerifier` as an application gate before current-user DPAPI unwrap when Hello is available. Windows 10 uses explicit DPAPI-only mode; the active mode is user-visible and Hello is not claimed to resist same-user malware.
7. Android biometric wrapping uses a non-exportable AES-256-GCM Android Keystore key authorized for every operation by strong biometrics and invalidated by biometric enrollment changes.

## Local Storage and Backups

1. Each client profile uses one SQLCipher database and one encrypted content-addressed blob directory.
2. The per-device SQLCipher key is random and separate from the password-derived Root Key. The platform keystore wraps it locally.
3. Daily retention means the newest seven valid daily backup sets once seven exist. Pre-migration and pre-restore safety backups are separate and do not consume daily slots.
4. A self-contained backup contains every encrypted blob referenced by its database snapshot. Hardlinks or trusted content-addressed reuse are optimizations, not correctness requirements.
5. Plaintext export is intentionally distinct from encrypted backup and always requires an explicit disclosure and confirmation.
6. The account's Root Key is retained in memory for the whole unlocked session, wiped on lock like every other live secret, because backup creation derives a fresh per-backup key from it on demand. It is not re-derived from the passphrase for each backup, which would otherwise require re-prompting on every scheduled daily backup.
7. Garbage collection's backup-awareness check is informational only: it reports whether a collection candidate is still present in an existing backup set, but never blocks collection on it. A backup set is a self-contained copy and never depends on live blob or note retention to remain restorable.
8. Selective restore always assigns freshly generated note IDs rather than reusing the backup's, and recreates any notebook/tag it does not find locally by name rather than requiring an exact structural match against the backup.

## Synchronization

1. Server sequence numbers are monotonic per workspace and are used only for incremental retrieval. HLC and CRDT state define merge behavior.
2. One worker owns synchronization for a workspace on a device; triggers coalesce instead of running concurrent pull/push cycles.
3. WebSocket notifications are cursor hints. Periodic and foreground pull remain authoritative.
4. An invalid operation blocks contiguous cursor progression and enters a visible quarantine workflow rather than being silently skipped.
5. Server compaction requires acknowledgements from all active devices or explicit revocation plus the retention boundary because the server cannot validate snapshot plaintext.
6. The transient LAN pairing transport can perform a complete on-demand peer synchronization but does not become a background home server.

## Server Operation

1. The primary server deployment is direct execution of one binary against one data directory.
2. A first run generates a self-signed certificate. Its fingerprint is transferred in the trusted connection QR and pinned by clients.
3. The server has one serialized SQLite writer. Parallel request parsing and reads do not imply parallel write transactions.
4. Metrics are disabled by default and never use user/resource identifiers as labels.
5. The default limits in `config.example.yaml` are sized for the documented household ceiling and are not promises of support above it.

## Build and Platform Validation

1. Go 1.26.3, Wails 2.14.0, Node.js 24, npm 11, Flutter 3.47.0, and Dart 3.13.0 are the phase-1 pinned toolchain.
2. `github.com/reearth/ygo` v1.48.0 and `golang.org/x/mobile` revision `1960c775504c` are the pinned CRDT and mobile-binding dependencies for the feasibility gate.
3. The SQLCipher feasibility adapter pins `github.com/AnoRebel/go-sqlcipher` v1.0.0 (SQLCipher 4.14.0). Its reviewed source is vendored so the Windows LLP64 pointer-width fix is reproducible and identical in desktop and Android builds.
4. Windows is authoritative for Wails and Android builds. The Android binding gate uses SDK platform 36, build-tools 36.0.0, and NDK 28.2.13676358; runtime acceptance requires an online `arm64-v8a` device.
5. AndroidX Biometric 1.1.0 is the stable compatibility layer used by the Flutter host for `BiometricPrompt.CryptoObject`; alpha APIs are not required.
6. The project-local `build/tools`, `build/.go`, and `build/.go-cache` directories are ignored developer conveniences. Clean builds must also work with documented tools installed on `PATH`.
7. Reference performance hardware and exact percentile methodology are recorded before performance acceptance gates run; measurements from unspecified hardware do not close those tasks.

## User Experience

1. Local-only onboarding is the selected default. Server connection remains available from settings.
2. Synchronization-disabled, offline, active, current, and failed are distinct user-visible states.
3. Mobile operating systems may delay background work indefinitely; foreground entry always resumes durable pending synchronization.
4. Revocation confirmation explicitly states that Beresta cannot erase data already copied by a formerly authorized device.
5. English and Russian are complete supported UI languages from the first user-facing implementation phase.

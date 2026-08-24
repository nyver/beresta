# Android privacy and storage

- `FLAG_SECURE` and Android 13 recent-task screenshot suppression protect every
  note-bearing activity. The app locks after the configured background delay.
- A non-exportable Android Keystore AES-256-GCM key wraps the mobile core device
  secret. A `BiometricPrompt` accepts strong biometrics or the configured device
  credential and gates each wrap/unwrap; after the first passphrase unlock it
  also protects a local envelope for the account Root Key. The key itself is not
  hardware-bound to the biometric result
  (`setUserAuthenticationRequired`), because several real Keymaster/TEE
  implementations throw `IllegalBlockSizeException` from `Cipher.doFinal()` on
  an AES/GCM key authorized through `BiometricPrompt.CryptoObject`.
- Notes, search indexes, revisions, sync journals, preferences, and cache policy
  live in SQLCipher. Attachment files and backups remain encrypted.
- Share and widget input is validated and encrypted before it touches private
  storage. The widget renders only a generic locked-state label.
- Content-URI attachments are bounded at 64 MiB and never copied to a plaintext
  filesystem cache. Backup provider URIs contain only encrypted backup sets.
- Attachment LRU eviction can select only redundant synchronized downloads.
  Pinned entries and unsynchronized local originals are protected.
- WorkManager progress contains only phase and retry counters. Notifications,
  logs, widgets, and recent-task surfaces never contain titles or note bodies.

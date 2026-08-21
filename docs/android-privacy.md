# Android privacy and storage

- `FLAG_SECURE` and Android 13 recent-task screenshot suppression protect every
  note-bearing activity. The app locks after the configured background delay.
- A non-exportable Android Keystore AES-256-GCM key wraps the mobile core device
  secret. Strong-biometric mode authorizes each unwrap and invalidates the key
  after biometric enrollment changes.
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

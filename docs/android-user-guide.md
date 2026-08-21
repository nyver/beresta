# Android user guide

Beresta creates and edits a complete local collection before any network is
configured. Create a local account or unlock the existing account with its
passphrase. Strong biometrics are used when the device supports them; Android
Keystore remains the device key boundary.

The navigation drawer filters the virtualized note list by notebook and shows
available tags. Search uses the encrypted local FTS index and supports the same
text/filter language as desktop. The editor saves Markdown through the shared
Yjs core; Preview is a derived view, while revisions can restore an older body
as a new current revision. Photo/document attachments are read from Android
content URIs into a bounded in-memory stream and encrypted immediately.

Local saves do not wait for a server. Foreground entry and constrained periodic
WorkManager jobs trigger pending synchronization when a server is configured.
Android may defer background work for battery or network policy; opening the
app always resumes it.

Use the cloud action to attach an optional HTTPS server. A first connection
accepts an invite code plus either a pinned SHA-256 certificate fingerprint or
a certificate trusted by Android. Disabling the server removes only runtime
transport state; the local collection and queued operations remain intact.

The backup action first asks for an Android document-tree destination. Beresta
creates and verifies the encrypted backup in private storage, copies it through
Storage Access Framework under a staging name, and publishes it only after all
files are present. Low-space or provider errors leave the prior valid backups
unchanged. “Import from destination” copies candidate backup sets back through
a bounded staging area, verifies the manifest and account-bound AEAD, and only
then adds them to the restore catalog.

Mobile settings control the automatic-lock interval, attachment retention
mode, selected notebooks, and encrypted attachment-cache limit. Pinned files
and unsynchronized local originals are never LRU candidates.

Android's Share sheet accepts bounded text/link and image input. Captures made
while locked are encrypted in private no-backup storage and imported only after
unlock. The quick-note widget follows the same path and never displays a note
title or body.

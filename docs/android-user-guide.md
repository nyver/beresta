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
The server URL, certificate fingerprint, and pinned/trusted choice are saved
on the device (not the one-time invite code), so reopening the server sheet
shows the same connection instead of a blank form, and a previously enabled
connection reattaches automatically the next time the account unlocks. The
cloud action's icon and the top of the server sheet reflect the live
synchronization status (not connected, offline and retrying, syncing, up to
date, or a sync error).

Each invite code registers its own independent workspace, so connecting a
second device with its own invite gives it its own empty collection on the
same server rather than the notes already on another device. To see the same
notes on two devices instead, both must first connect to the same server (each
with its own invite code), then share the workspace between them:

1. On the device joining an existing workspace, open the server sheet and
   copy "Your identity code".
2. Send that code to whoever owns the workspace, over any channel you trust
   (the code identifies the joining device but reveals nothing about its
   notes).
3. On the owning device, paste the code into "Share this workspace" and copy
   the resulting grant code back to the joining device.
4. On the joining device, paste the grant code into "Join a shared
   workspace." The server sheet keeps the device on the synchronization
   status until its initial download completes; then that workspace becomes
   active for the rest of this session, alongside the device's own original
   one. The same sheet lists every workspace held by the device and lets you
   switch between them.

This makes the joining device a workspace member (not the workspace owner),
matching the up-to-five-user household model. Only the owner can share a
workspace it holds.

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

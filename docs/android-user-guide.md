# Android user guide

Beresta creates and edits a complete local collection before any network is
configured. Create a local account or unlock the existing account with its
passphrase once. On a device with a secure screen lock, later launches can use
strong biometrics or the configured device credential (PIN, pattern, or device
password); the passphrase remains available as the recovery fallback. Android
Keystore remains the device key boundary and the app never stores the
passphrase.

The navigation drawer filters the virtualized note list by notebook and shows
available tags. Search uses the encrypted local FTS index and supports the same
text/filter language as desktop. The editor is WYSIWYG (bold/italic/strike/
inline code, headings, ordered/bullet lists, blockquotes, code blocks, and
links, matching desktop's own formatting toolbar) rather than raw Markdown
source with a separate preview: it saves through the shared Yjs core's
canonical Markdown projection, so formatting made on either client survives a
sync to the other. Revisions can restore an older body as a new current
revision. Photo/document attachments are read from Android content URIs into
a bounded in-memory stream and encrypted immediately.

Local saves do not wait for a server. Foreground entry and constrained periodic
WorkManager jobs trigger pending synchronization when a server is configured.
Android may defer background work for battery or network policy; opening the
app always resumes it.

Use the cloud action to attach an optional HTTPS server. The simple path
accepts one pasted connection code (a `beresta://connect` link bundling the
server URL, invite, TLS policy, and certificate fingerprint, delivered as a QR
image or copied text); an "Advanced connection setup" section reveals the URL,
invite code, and pinned SHA-256 fingerprint/Android-trusted certificate choice
individually for setups without a connection code to paste. Disabling the
server removes only runtime transport state; the local collection and queued
operations remain intact.
The server URL, certificate fingerprint, and pinned/trusted choice are saved
on the device (not the one-time invite code), so reopening the server sheet
shows the same connection instead of a blank form, and a previously enabled
connection reattaches automatically the next time the account unlocks. The
sheet identifies the active server and its HTTPS/TLS 1.3 verification policy
and can apply a replacement URL or certificate policy without disabling the
current connection first. A failed replacement leaves the working connection
and its saved settings unchanged. The cloud action's icon and the top of the
server sheet reflect the live
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
   workspace." The owner publishes the existing collection immediately, and
   the server sheet keeps the device on the synchronization status until its
   initial download completes; then that workspace becomes
   active across future lock/unlock cycles, alongside the device's own
   original one. The same sheet lists every workspace held by the device and
   lets you
   switch between them.

This makes the joining device a workspace member (not the workspace owner),
matching the up-to-five-user household model. Only the owner can share a
workspace it holds. Sharing, joining, and disconnecting a device or member
each stage through an explicit confirmation step before the underlying
action runs, and every code (identity, grant, connection) renders as a QR
image by default with the raw opaque text available behind a "Show code"
toggle, so the primary flow never surfaces key material directly.

The Devices section of Settings (see below) lists every device with
access to the current workspace by platform and understandable name, when
it was last seen, whether it is the current device, and its access state
(Active or Revoked); disconnecting one requires confirming the same
future-access-only limitation desktop shows - revocation stops a device's
future synchronization but cannot erase content it already downloaded.
Revoking a workspace member automatically rotates that workspace's key to
every remaining device; this happens without any visible primary-flow
state change, though it is recorded in Diagnostics' technical details if
a rotation is still pending.

The backup action first asks for an Android document-tree destination. Beresta
creates and verifies the encrypted backup in private storage, copies it through
Storage Access Framework under a staging name, and publishes it only after all
files are present. Low-space or provider errors leave the prior valid backups
unchanged. “Import from destination” copies candidate backup sets back through
a bounded staging area, verifies the manifest and account-bound AEAD, and only
then adds them to the restore catalog.

Settings is one grouped bottom sheet with six sections - General, Security,
Synchronization, Data, Advanced, and About - rather than separate backup,
server, and settings buttons on the app bar. Security holds the automatic-
lock interval and device unlock; Synchronization holds the cloud/device/
sharing actions described above; Data holds backup, restore, and
import/export, including attachment retention mode, selected notebooks,
and the encrypted attachment-cache limit (pinned files and unsynchronized
local originals are never LRU candidates); Advanced holds one "Check my
data" action that runs a consolidated local-database-integrity, search-
index-consistency, and backup-health check and reports either "No problems
found" or a single actionable summary, never the individual maintenance
jobs it runs on the user's behalf.

Android's Share sheet accepts bounded text/link and image input. Captures made
while locked are encrypted in private no-backup storage and imported only after
unlock, from whichever of passphrase, biometric, or device-credential path the
user actually completes; a one-time notice on the note list reports how many
shared items were added once they are. The quick-note widget follows the same
path and never displays a note title or body; an in-progress draft survives a
rotation or low-memory process recreation instead of being silently lost.

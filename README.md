# Beresta

Beresta is an offline-first encrypted notes application for Windows and Android with an optional single-binary home synchronization server. Clients are complete local applications: the server is transport, not the authority for user data.

## Project Status

Beresta has completed the Windows desktop, home-server, HTTP synchronization,
and Android application phases. Phases
1 and 2A delivered the architecture baseline and cryptographic core; phase 2B
added the encrypted client store; phase 3 added the complete offline note
application surface through `core/account`; phase 4 wires that surface into a
packaged Windows application; the following phases added the optional opaque
home server, convergent client synchronization, and the Android application:

- a buildable Wails v2 Windows host with a React/TypeScript frontend;
- a Flutter Android application with local onboarding/unlock, a nested notebook hierarchy (create/delete, move notes between notebooks), tag management (create/delete, assign/unassign per note, filter the note list by tag), virtualized notes, Markdown source editing with a rendered-Markdown preview toggle, attachments with an inline thumbnail/preview strip and per-attachment deletion, search, revisions, secure lifecycle handling, background synchronization, encrypted SAF backups, share capture, and a private quick-note widget;
- a Yjs V1/V2 adapter plus a SQLCipher 4.14 encrypted-database probe whose Android AAR is produced by `gomobile bind`;
- owned mutable secret buffers, device-bounded Argon2id, domain-separated HKDF, X25519/Ed25519 identities, XChaCha20-Poly1305 keybag/object/attachment/backup encryption, and the shared Windows DPAPI and Android Keystore/biometric key-wrapping contract (phase 2A);
- validated UUIDv7 identifiers, Hybrid Logical Clock persistence, and deterministic last-writer-wins registers with device-ID and logical-counter tie breaks;
- the complete embedded client schema (accounts, devices, workspaces, notebooks, tags, notes, CRDT state/updates, revisions, attachments, inbox/outbox, cursors, snapshots, backups, saved searches, FTS5) under SQLCipher with WAL, foreign keys, and transactional migrations;
- account creation/unlock/lock services that derive every local key without network access, and the default local no-op `SyncTransport`;
- note/notebook/tag/attachment application services (`core/account`) that commit local state and a signed encrypted outbox operation atomically, the `DocumentCRDT` rich-text adapter with canonical Markdown projection, and seven-day note revision history with periodic checkpoints, a line-based diff, and rollback-as-new-revision;
- local full-text search with title/body ranking, tag/date filters, saved queries, and cancellation, meeting the 150 ms budget on a 20,000-note fixture;
- an encrypted content-addressed blob store (write-temp/fsync/rename publication, content-addressed deduplication, transactional note references, and orphan grace-period tracking) for attachments;
- daily encrypted client backups (zstd-compressed, encrypted under a Root-Key-derived backup key, self-contained attachment blob sets, exact seven-day rotation, missed-day catch-up, startup/pre-restore verification with corruption classification, and capacity preflight);
- backup catalog preview, a dry-run restore change plan, atomic whole-database restore (onto a freshly generated device key, with crash-safe rollback to the original database on failure), and selective restore as new local operations, always behind a mandatory pre-restore safety backup;
- confirmed plaintext Markdown/attachment/`manifest.json` export, and import of both Beresta's own portable archives and Evernote `.enex` files, with a user-visible report of anything that could not be represented (rich text is flattened to plain text on import — see [ASSUMPTIONS.md](ASSUMPTIONS.md));
- blob and tombstone garbage collection at the 30-day minimum retention floor, with dry-run reporting and backup-awareness (informational, never blocking: a backup set is self-contained);
- a headless end-to-end suite covering the complete local-only lifecycle, including a real forced-termination (process-kill) recovery test and randomized local-operation/restore-convergence property tests;
- schema-migration safety backups taken automatically before a pending migration runs, an FTS index rebuild primitive, and a tested backup/restore round trip as the forward-fix recovery path;
- build-time validation for source English and Russian localization catalogs;
- architecture, threat-model, crypto, synchronization, and ADR documentation;
- one root verification command and a Windows CI workflow;
- a `beresta-server` executable with strict configuration defaults, a
  private single-directory data lifecycle, embedded transactional migrations,
  and an atomically generated self-signed TLS identity with a stable SHA-256
  fingerprint; invite-only registration, server-bound Ed25519 challenge
  sessions, resource-scoped `/v1` APIs, opaque operation/blob/snapshot storage,
  bounded WebSocket hints, verified seven-day backups, and the administration
  CLI are implemented without an external service or cgo dependency.
- deterministic-CBOR operation envelopes, durable pull-verify-apply-then-push workers, exactly-once application/quarantine recovery, TLS 1.3 pinning, resumable encrypted blobs, encrypted snapshots that also carry notebook/tag/attachment catalogs, compaction bootstrap, and functional Windows server/device diagnostics;
- a gomobile-safe value API with bounded event polling and cancellation, reproducibly normalized/checksummed Android AARs, Android Keystore fast re-entry through biometrics or the device PIN without storing the account passphrase, constrained WorkManager jobs, SPAKE2-confirmed LAN frames, encrypted share/widget handoff, and attachment-cache retention controls;
- workspace sharing (per-recipient sealed X25519 key envelopes with client-side membership signature verification), signed member/device revocation, no-downtime workspace-key rotation with historical-key reads and resumable local re-encryption hardening, and an optional shared-folder transport (immutable operation segments, a short-locked manifest, and content-addressed blob exchange) as an HTTP alternative;
- desktop and mobile UI for that sharing primitive: an `ExportIdentity`/`ShareWorkspace`/`AcceptWorkspaceGrant`/`ListWorkspaces`/`SetActiveWorkspace` surface (`core/sharecode`'s opaque `beresta://identity` and `beresta://grant` out-of-band codes) so a second already-registered household device can join and switch to an existing workspace instead of only ever owning its own; sharing prompts the owner to publish its existing collection, including notebooks, tags, attachment manifests, and verified encrypted attachment chunks, mobile preserves its selected workspace across lock/unlock and synchronizes it on launch, lifecycle boundaries, local changes, and manual refresh, lists notes by last modification time, and its notebook menu can create a root-level note, while desktop provides a topbar force-sync control for the current workspace and both clients display bounded diagnostics for a failed sync cycle; desktop owners can also see and disconnect active workspace clients (see [docs/sync-protocol.md](docs/sync-protocol.md) and [docs/android-user-guide.md](docs/android-user-guide.md));
- local operation-log and snapshot garbage collection alongside the existing tombstone/blob collector; systemd, Windows batch/Task Scheduler, and optional Docker deployment assets; and a release pipeline covering signed update-manifest publication, Android release signing, server checksums/SBOM/provenance, a core coverage gate, and `govulncheck`/OSV dependency scanning.

The completed Phase 4 Windows desktop application uses a coarse Wails
application service layer is implemented (`desktop/app.go` and its sibling
files): account lifecycle, note/notebook/tag commands, search and saved
searches, revision history, attachments (via native file pickers, since the
JS bridge cannot carry Go `io.Reader`/`io.Writer` streams), backup/restore,
import/export, garbage collection, desktop settings, and the English/Russian
locale catalogs are all bound to the frontend as JSON-safe methods that never
expose the raw database handle or key material, plus `account:unlocked`,
`account:locked`, and `sync:status` events. Every bound method's failure is a
stable `{code, message}` pair (`desktop/errors.go`'s `AppError`, JSON-encoded
because Wails only ever transmits an error's plain string across the JS
bridge) so the frontend can localize and branch on `code` instead of
matching English backend text.

The first screens built on top of that layer are now in place: English/Russian
onboarding with "Only on this computer" selected by default and a "Connect
to server" path accepting an invite or trusted connection QR without blocking
local account creation, a returning-user unlock screen (chosen automatically when
a previous local account is on record), and a main shell with a keyboard-
accessible notebook tree, tag navigation (via a dedicated `SearchByTag`
binding that reuses the same search index as the search box, without its
text-query quoting limitations), and a virtualized note list
(`@tanstack/react-virtual`, since the account ceiling is 20,000 notes)
selecting into a working note editor. Notebook, note, and tag creation and
deletion live behind a per-row "⋮" kebab menu (`shell/KebabMenu.tsx`) next
to each item's name rather than always-visible buttons. Choosing "New note"
from a notebook's menu files it directly in that notebook; the Notebooks
section menu creates a note in the workspace root. Both trees
support drag-and-drop reorganization: dropping a notebook onto another
reparents it (and everything filed under it) via the existing
`MoveNotebook` cycle-checked binding, and dragging a note from the list
onto a notebook row refiles it via `SetNoteNotebook` - both are plain
reparenting/refiling, since neither `store.Notebook` nor `model.Note`
carries a persisted sibling order to reorder within a level. Tags
themselves are created from the sidebar's tag list (behind a "+" toggle,
mirroring the notebook tree's own inline-create pattern), and assigned to
or removed from the open note via a chip editor in the note header
(`shell/NoteTagsEditor.tsx`, backed by `App.NoteTagsByWorkspace` and the
existing `SetNoteTag`/`CreateTag` bindings) - previously tags could only be
browsed and filtered, never created or attached from the UI. The editor is
Quill 2 bound to the
note's Yjs `Y.Text` through `y-quill` - matching `core/sync/yjsadapter`'s
own Quill-Delta-compatible document model exactly, so the toolbar is
deliberately restricted to the formatting marks the Go core's canonical
Markdown projection understands. Local edits are captured as incremental
Yjs updates, debounced, merged, and committed through `CommitNoteBody`;
they flush immediately (not debounced) when a note closes or the account
locks, and a failed commit is retried rather than dropped. Attachments are
fully wired into the editor pane: native drag-and-drop (Wails'
`OnFileDrop`, scoped to the attachment panel via the `--wails-drop-target`
CSS marker) and clipboard image paste (intercepted before Quill's own
clipboard module can turn it into an unexportable inline blot — see
`docs/architecture.md`) both feed the same upload queue as the "Attach
file…" picker button; a still-queued item can be canceled, image
attachments get an inline decrypt-to-memory preview capped at 8 MiB (never
written to disk, matching the no-plaintext-attachment-cache rule below),
clicking a thumbnail opens it enlarged in a lightbox `Modal` reusing that
same already-decrypted preview, and "Save as…"/"Remove" now live behind
each row's kebab menu, decrypting straight to a user-chosen destination.
The note list's search box leads with a plain text field; the tag/date/
include-deleted filter controls and saved-search management are collapsed
behind a "Filters & saved searches" disclosure by default so they do not
crowd the sidebar. With no such filter engaged, typing free text matches
notes by partial, case-insensitive note title entirely client-side against
the already-loaded note list - instant, and able to match mid-word,
unlike the backend FTS5 index's whole-token-only `unicode61` tokenizer.
Engaging any filter (or typing a query already containing one of the
`tag:`/`after:`/`before:`/`deleted:true` tokens directly, as a loaded saved
search's stored query does) instead composes free text with those filters
into that same query language and runs it through the debounced (~150 ms)
`App.Search` → `core/account.Search` → `store.SearchNotes` path that
`core/store`'s 20,000-note / 150 ms budget test already benchmarks (see
above), so that budget covers this UI's queries too; `SavedSearch` stores
the composed query verbatim so saved searches round-trip through the same
box. Either way, an active search overrides the sidebar's notebook/tag
browsing (cleared by picking a notebook or tag, or by the box's own Clear
button) and highlights its matched free-text term in each result's title.
A note's history
panel lists its retained revisions (newest first, checkpoints marked),
diffs the selected one against its predecessor, and can restore it as a
new current revision without erasing anything in between - restoring
first flushes any not-yet-debounced body edit still queued in the open
editor, so that stale edit cannot get silently re-committed on top of the
just-restored content when the editor remounts afterward. A "Backups &
Data" dialog off the shell's topbar covers the rest of task 5.7: the
external backup directory setting (with a native folder picker,
defaulting under the app data directory but movable to any external
location), a manual "back up now" action, a catalog tabbed by kind
(daily/manual/pre-restore/pre-migration) with per-backup verify and
preview, and a dry-run restore plan listing each note's classification
(new/updated/unchanged) before committing to either "restore selected as
new notes" (`RestoreSelective`) or the separately confirmed, destructive
"replace everything with this backup" (`RestoreWhole`); the same dialog's
import/export section requires an explicit warning acknowledgement before
a plaintext export (notes and attachments leave the encrypted store as
plain files) and surfaces per-note warnings from a Beresta-archive or
Evernote `.enex` import. The topbar also carries a configurable auto-lock
timeout (never/5/15/30/60 minutes, backed by `AppSettings.AutoLockMinutes`
and reset by any keyboard/mouse activity) and a badge reflecting the
account's actual key-protection mode (`AccountInfo.KeyProtection`, already
DPAPI-gated at the crypto layer since phase 2A - this badge and
the timeout are what task 5.8 adds on top of that existing protection, not
a new unlock mechanism). Locking - whether by the topbar button or the
idle timeout - immediately swaps the note-bearing shell body for a neutral
"Locking…" placeholder before the flush/lock calls even resolve, so no
note content is still on screen during that transition (task 5.8's
"secure content hiding while locked"). The returning-user unlock screen
also offers a local-wipe workflow behind a typed `ERASE` confirmation:
`account.EraseLocalAccount` deletes the database, its key envelope, and
the attachment blob store for a device that cannot or should not be
unlocked normally - the windows-desktop-client spec's revocation-response
primitive, usable today as a manual reset since no sync transport exists
yet to deliver an actual revocation signal. The desktop app also runs a
native system-tray icon - extracted from the running executable's own
embedded icon (`shell32.dll`'s `ExtractIconExW`) rather than a generic
stock icon, so it matches the app the same way the taskbar and Alt+Tab
already do - with a "Quick Note" / "Show Beresta" / "Quit" context menu, a
configurable global quick-note hotkey (default
`Ctrl+Shift+N`, changeable from the Backups & Data dialog) that opens a
focused capture surface and brings the main window forward even while it
is hidden, and an opt-in "launch at sign-in" autostart toggle backed by
the per-user Windows Run key, with a UI warning when a stale entry from a
different install path is detected. Closing the main window hides it to
the tray instead of exiting whenever the tray started successfully; the
tray menu's "Quit" (or a failed tray/hotkey startup, logged to the
console) is what actually ends the process. A named single-instance lock
held for the process lifetime stops a second launch from opening a
duplicate window, tray icon, and hotkey registration; launching again
while Beresta is already running instead brings the existing main window
to the foreground (or, if it is hidden to the tray, leaves it there - the
tray icon is already the right way to reach it). A separate Synchronization
entry now renders the shared disabled/offline/active/current/failed state
model and listens for `sync:status` events without blocking local editing.
In the current local-only phase it truthfully shows the active local device,
an empty conflict/quarantine journal, and disabled server/device-management
controls rather than fabricating remote state. Windows packaging now produces
a per-user NSIS installer with explicit preserve/delete-local-data uninstall
choices, a branded application icon, and a fail-closed updater that verifies
the pinned release signature, SHA-256 digest, Authenticode publisher trust, and
the installed executable before retaining or restoring the prior version.
Desktop component/accessibility/Wails-boundary tests, an offline end-to-end
scenario, installer/update tests, the 20,000-note search benchmark, and the
ten-launch cold-start gate now cover the phase-available behavior.

The shell's visual design leans further into "offline-first, state over
buttons": the topbar's old full-width Sync button is now a compact,
color-coded status pill (reusing the same disabled/offline/active/current/
failed palette as the Synchronization dialog's own status card) that opens
that same dialog on click, Settings is an icon-only gear, and the Windows
key-protection indicator reads as a small muted hint instead of a
pill that looks pressable. The three-pane layout (navigation/notes/editor)
now separates its panes with a background tint step instead of a vertical
rule between every column - only the notes-list/editor boundary keeps an
actual border - and narrows navigation and the note list to make more room
for the editor; a "☰" toggle collapses navigation (also bound to
Ctrl/Cmd+Shift+S) and a "⛶" toggle enters a distraction-free focus mode
(editor only), both persisted in `localStorage` across launches. The note
list's preview snippet is now rendered as plain text (Markdown syntax
stripped client-side - the stored preview itself is still the canonical
Markdown source, so this is display-only), the last-modified date sits
beside the title instead of crowding the preview line, and the selected
row uses a lighter accent tint plus a left accent bar rather than a solid
highlight block, so it scales better once a notebook holds dozens of notes.
Tag creation in the sidebar is now hidden behind a "+" toggle (matching the
notebook tree's existing pattern) instead of a permanently visible input.
The open note's title renders larger, and its History action moved into
the "⋮" menu alongside New note/Delete instead of sitting next to the title
as a permanent text link; the Quill toolbar and editor area lost their
surrounding box borders (a bottom rule under the toolbar is the only
remaining separator), and its heading dropdown's default option now reads
"Paragraph" instead of Quill's own default "Normal". A footer status line
under the editor - "Saved locally", combined with the workspace's live
synchronization state ("· Offline", "· Syncing…", "· Synced HH:MM", or a
clickable "· Sync failed" that opens the Synchronization dialog) - replaces
relying on the topbar Sync button alone to know whether an edit is safe.

The completed phase-1 build matrix, test scope, review findings, and limitations
are recorded in [the phase-1 delivery report](docs/phase-1-report.md).
Cryptographic/platform protection verification is recorded in
[the phase-2A delivery report](docs/phase-2a-report.md), the encrypted
local store is recorded in
[the phase-2B delivery report](docs/phase-2b-report.md), and the complete
local-only product core is recorded in
[the phase-3 delivery report](docs/phase-3-report.md), and the packaged Windows
application is recorded in
[the phase-4 delivery report](docs/phase-4-report.md).
HTTP synchronization is recorded in
[the phase-6 delivery report](docs/phase-6-report.md), and Android delivery is
recorded in [the phase-7 delivery report](docs/phase-7-report.md).

The desktop and Android clients store complete local collections and can attach
to the optional server without migrating data. Network failure changes only the
visible synchronization state; local editing and queued operations remain available.

## Security Model

- Notes, metadata, attachments, revisions, backups, and synchronization payloads are encrypted on clients.
- Passwords, Root Keys, unwrapped workspace keys, plaintext content, and plaintext search terms never reach the optional server.
- Every client retains a complete local copy and remains usable when the server is absent.
- The server stores opaque signed operations and encrypted blobs in SQLite plus a local directory; it needs no PostgreSQL, Redis, S3, queue, or orchestrator.
- Device revocation protects future access but cannot erase data already copied by a formerly authorized device.

The normative design is documented in [architecture.md](docs/architecture.md), [threat-model.md](docs/threat-model.md), [crypto-spec.md](docs/crypto-spec.md), and [sync-protocol.md](docs/sync-protocol.md).

## Repository Layout

| Path | Responsibility |
|---|---|
| `core/` | Shared Go model, cryptography, encrypted store, backup, synchronization, transports, and mobile binding API |
| `desktop/` | Wails v2 process and React/TypeScript Windows UI |
| `mobile/` | Flutter Android UI and platform wrapper |
| `server/` | Optional Go home synchronization server and CLI |
| `locales/` | Embedded English/Russian UI string catalogs shared by clients |
| `schema/` | Versioned wire/storage formats and compatibility fixtures |
| `docs/` | Architecture, threat model, crypto/sync specifications, and ADRs |
| `build/` | Build, packaging, CI, and deployment assets |

## Prerequisites

The phase-1 reference environment is:

- Windows 10/11 amd64;
- Go 1.26.3;
- Node.js 24 and npm 11;
- Wails CLI 2.14.0;
- NSIS 3.12 (`makensis.exe`) for Windows installer packaging;
- Flutter 3.47.0 with Dart 3.13.0;
- `gomobile`/`gobind` from `golang.org/x/mobile` revision `1960c775504c` for native core bindings;
- a Windows amd64 GCC toolchain for the CGo SQLCipher build (w64devkit 2.9.0 is the reference toolchain);
- Microsoft WebView2 Runtime.

Install Wails with:

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.14.0
```

Install Flutter 3.47.0 from the official Flutter distribution and place `flutter` and `dart` on `PATH`. The root build also discovers project-local tools at `build/tools/wails.exe` and `build/tools/flutter-sdk/bin/`; that directory is ignored by Git.

Install a Windows amd64 GCC toolchain on `PATH`, or unpack w64devkit 2.9.0 to `build/tools/w64devkit`. The latter path is discovered automatically and remains ignored by Git. The reviewed SQLCipher 4.14.0 amalgamation is pinned under `third_party/go-sqlcipher`; its retained upstream license and Windows LLP64 compatibility note are in `third_party/`.

Install the pinned binding tool into the project-local Go tool directory when building native mobile artifacts:

```powershell
$env:GOBIN = "$PWD\build\.go\bin"
go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260813181013-1960c775504c
```

Android packaging additionally requires Android SDK platform 36, build-tools 36.0.0, NDK 28.2.13676358, and accepted licenses. Set `ANDROID_SDK_ROOT`/`ANDROID_HOME`, or configure the SDK in Flutter before invoking the root binding command.

Example Android configuration:

```powershell
$env:ANDROID_SDK_ROOT = "D:\Android\Sdk"
$env:ANDROID_HOME = $env:ANDROID_SDK_ROOT
flutter config --android-sdk $env:ANDROID_SDK_ROOT
build.cmd mobile-bind-android
```

## Bootstrap

From a clean checkout:

```powershell
build.cmd bootstrap
build.cmd verify
```

`build.cmd` uses a versioned PowerShell implementation and works on machines whose normal PowerShell execution policy blocks local scripts. It keeps Go, Gradle, Dart/Flutter, and pub caches under ignored `build/` paths, so verification does not require write access to the user's global profile directories.

## Build and Verification Commands

Run all commands from the repository root:

```powershell
build.cmd format
build.cmd format-check
build.cmd locale-check
build.cmd lint
build.cmd test
build.cmd coverage-gate
build.cmd security-scan
build.cmd build
build.cmd server-build
build.cmd server-cross-build
build.cmd server-smoke
build.cmd package
build.cmd cold-start
build.cmd installer-smoke
build.cmd mobile-check
build.cmd mobile-bind-android
build.cmd mobile-build-android
build.cmd mobile-package-android
build.cmd mobile-test-android
build.cmd verify
```

The commands mean:

| Command | Action |
|---|---|
| `bootstrap` | Install locked npm packages and resolve Flutter packages in the project-local cache |
| `format` | Apply Go and Dart formatters |
| `format-check` | Fail if checked Go or Dart sources need formatting |
| `locale-check` | Reject missing, duplicate, empty, or untranslated English/Russian catalog entries |
| `lint` | Validate localization catalogs, run `go vet`, TypeScript type checking, and Flutter analysis |
| `test` | Run Go, Vitest, and Flutter tests |
| `coverage-gate` | Run `core/...` tests with coverage and fail below the release-quality spec's 80% floor |
| `security-scan` | Run `govulncheck` and an OSV dependency scan against `go.mod` |
| `build` | Build the server, React bundle, and Windows Wails executable |
| `server-build` | Build `build/output/beresta-server.exe` for the current host |
| `server-cross-build` | Build static Windows amd64, Linux amd64, and Linux arm64 server binaries under `build/output/server/`, plus `SHA256SUMS`, a per-binary module manifest, and `provenance.json` |
| `server-smoke` | Cross-build all server targets and smoke-test Windows first start plus live-state verification |
| `package` | Build the app, fail-closed updater, and per-user NSIS installer under ignored `build/output/` |
| `cold-start` | Measure ten fresh-profile launches to the first responsive Windows main window and enforce a five-second nearest-rank p95 budget |
| `installer-smoke` | Exercise install, update preservation, default data retention, and explicit purge in a disposable Windows profile |
| `mobile-check` | Generate and validate the gomobile Java binding surface |
| `mobile-bind-android` | Produce `build/output/beresta-core.aar`; requires Android SDK/NDK |
| `mobile-build-android` | Produce the Android AAR and a Flutter debug APK with native SQLCipher linkage |
| `mobile-package-android` | Produce a signed release APK and AAB (requires `BERESTA_ANDROID_KEYSTORE_*`; see [docs/android-build.md](docs/android-build.md)) |
| `mobile-test-android` | Run the SQLCipher instrumentation round trip on a connected `arm64-v8a` Android device |
| `verify` | Run format checks, lint, tests, and package the phase-available artifacts |

The server, Windows executable, updater, and installer are copied to
`build/output/beresta-server.exe`,
`build/output/beresta.exe`, `build/output/beresta-updater.exe`, and
`build/output/Beresta-amd64-installer.exe`. Generated output is ignored. See
[desktop update and installer operations](docs/desktop-updates.md) for release
signing and Windows 10/11 smoke-test requirements.

## Module-Specific Commands

### Go

The client storage layer links the vendored SQLCipher amalgamation, whose FTS5
support is only compiled when the `sqlite_fts5` build tag is set. Bare
`go test ./...` therefore fails with `no such module: fts5`. `build.ps1` exports
the required environment for you, so prefer:

```powershell
.\build.ps1 test
```

To run the Go tools directly, export the same settings first:

```powershell
$env:CGO_ENABLED = "1"
$env:GOFLAGS = "-tags=sqlite_fts5"
go test ./core/... ./server/... ./internal/...
go vet ./core/... ./server/... ./internal/...
```

A C compiler must be on `PATH`; `.\build.ps1 bootstrap` provisions a portable
one into `build/tools/w64devkit`. Package patterns are listed explicitly because
a populated server data directory under `cmd/` is created with restrictive ACLs
and makes `./...` expansion fail with `Access is denied`.

Initialize the optional server data directory without starting its listener:

```powershell
go run ./cmd/beresta-server --data ./data --init-only
```

Remove `--init-only` to listen on the configured HTTPS address. See the
[server API](docs/server-api.md) and [operations guide](docs/server-operations.md)
for enrollment, administration, backup/restore, service, container, and
Raspberry Pi commands.

Publish a signed desktop update manifest, or a detached signature over any
other release file (see [desktop update operations](docs/desktop-updates.md)):

```powershell
$env:BERESTA_RELEASE_PRIVATE_KEY_BASE64 = "<base64 64-byte Ed25519 private key>"
go run ./cmd/beresta-release-sign -artifact build/output/Beresta-amd64-installer.exe -version 1.2.0
go run ./cmd/beresta-release-sign -detached-file build/output/server/SHA256SUMS
```

### Desktop frontend

```powershell
cd desktop/frontend
npm ci
npm run typecheck
npm test
npm run build
```

### Wails desktop

```powershell
cd desktop
wails doctor
wails build -clean
```

### Flutter mobile

```powershell
cd mobile
flutter pub get
flutter analyze
flutter test
```

Flutter Android packaging and native core binding commands are available at the repository root. `mobile-test-android` requires an online physical `arm64-v8a` device with USB debugging enabled and exercises SQLCipher, Android Keystore/biometric wrapping, secure-window behavior, encrypted capture, and bounded background-work integration.

The shared Yjs adapter accepts official Yjs V1 and V2 updates behind `core/sync/yjsadapter`; `core/mobileapi` exposes only gomobile-safe values, request cancellation, and bounded event polling. `mobile-bind-android` normalizes ZIP/JAR metadata and writes `beresta-core.aar.sha256` beside the SQLCipher-linked AAR. The Flutter host calls the same account, search, revision, backup, and synchronization services as desktop rather than maintaining a second persistence model.

Local database keys use the versioned `BKW1` envelope from `core/keystore`, with key ID and purpose bound to the OS protection operation. Windows always uses the explicit, user-scoped DPAPI protection mode (`CryptProtectData`/`CryptUnprotectData`, no OS UI). An earlier Windows Hello-gated mode (`UserConsentVerifier` via a small C shim) was removed after `RequestVerificationForWindowAsync` reliably crashed the process the instant the user completed verification - reproduced with two independently correct native consumption patterns (`get_Status` polling and `put_Completed`/`CoWaitForMultipleHandles`), pointing at a platform-level issue rather than something fixable in this codebase. Android stores non-exportable AES-256-GCM wrapping keys in Android Keystore and gates them with the system `BiometricPrompt`, accepting strong biometrics or the configured device credential (PIN, pattern, or device password). After the first successful passphrase unlock, the app wraps the account Root Key with that device-bound secret, so later mobile launches can unlock without storing or retransmitting the passphrase; the passphrase remains the recovery fallback.

English and Russian source strings live in `locales/en.json` and `locales/ru.json`. The root build validates both catalogs before linting and building, including explicit duplicate-key detection that ordinary JSON decoding does not provide.

## Configuration

The optional server will run with safe defaults and no configuration file:

```powershell
beresta-server --data ./data
```

Copy [config.example.yaml](config.example.yaml) to `<data>/config.yaml` only when
changing defaults, or pass an explicit file with `--config`. The `--data` value
always overrides `server.data_dir`. `config.yaml` is intentionally ignored
because it may contain local paths or deployment-specific certificate
information. On first initialization the server creates `beresta.db`, `blobs/`,
`backups/`, and an atomically published `tls/` identity beneath the private data
root. Record the printed SHA-256 fingerprint through a trusted channel before
clients pin it.

Create the first single-use invite after initialization:

```powershell
beresta-server --data ./data invite --name Alice
```

Global flags precede administrative commands. Restore and garbage collection
are dry runs unless `--confirm` is supplied. Daily server backups contain a
verified SQLite snapshot and immutable encrypted blob view; exactly the newest
seven valid daily backups are retained.

## Development Rules

- Keep user-visible strings in English/Russian localization catalogs.
- Keep code comments and configuration comments in English.
- Do not introduce external server infrastructure or abstractions for workloads above the documented five-user ceiling.
- Do not log content, passwords, keys, authorization tokens, invite codes, or arbitrary request/response objects.
- Update this README and `config.example.yaml` whenever a phase adds user-visible functionality or configuration.
- Every phase must compile, pass its available tests, complete a lead-level security/performance/UX review, and record known limitations.
- Architecture or security deviations require an ADR before implementation.

## Current Limitations

- The Windows cold-start gate is reference-host-sensitive: one run immediately after packaging failed because a single native WebView2 initialization took 6569 ms, while the repeated acceptance run on the idle reference host passed at p95 3755 ms. The ten-sample method, measured constraint, and revision from the original 1.5-second budget are recorded in [ADR 0007](docs/adr/0007-desktop-cold-start-budget.md); hosted CI timing does not substitute for this gate.
- Android camera/document attachments enter the core from a bounded content-URI stream and never use a plaintext cache path. External plaintext "open with" remains intentionally unavailable; an explicit export is required.
- `quill@2.0.3` (the current stable release) has an open low-severity XSS advisory in its HTML-export feature ([GHSA-v3m3-f69x-jf25](https://github.com/advisories/GHSA-v3m3-f69x-jf25)); Beresta never calls that feature (`getSemanticHTML`/HTML clipboard export), relying only on the Delta model and the Go core's own Markdown projection, so it is not reachable through anything this app does.
- HTTP and folder synchronization, workspace sharing, member/device
  revocation, and no-downtime workspace-key rotation are implemented and
  covered by real end-to-end tests against a live server (see
  `server/sharing_e2e_test.go`). Desktop and mobile now expose sharing/joining
  a workspace and switching the active one (`ExportIdentity`/`ShareWorkspace`/
  `AcceptWorkspaceGrant`/`ListWorkspaces`/`SetActiveWorkspace`, covered by
  `desktop/workspaces_test.go` and `core/mobileapi/sharing_test.go` against a
  real in-process server). Desktop owners can list and disconnect active
  workspace members; removal blocks their future server synchronization but
  cannot erase notes or keys they already downloaded. Mobile's
  active-workspace choice is stored in the persisted `mobile_preferences`
  row, so the most recently selected workspace is restored after every
  unlock, matching desktop's `AppSettings.ActiveWorkspaceID` behavior. The
  Kotlin `MainActivity.kt`
  method-channel wiring for the new bridge calls was hand-verified against
  the established 1:1 naming convention with the other bound methods but not
  compiled, since this development environment has no configured Android SDK
  (`ANDROID_SDK_ROOT`); run `build.cmd mobile-bind-android` and
  `build.cmd mobile-build-android` on a host with the Android toolchain
  before shipping. Raspberry Pi idle acceptance runs only on the opt-in
  `beresta-pi` reference runner; ordinary hosted CI cross-builds Linux arm64
  but cannot substitute for the physical RSS/CPU measurement.
- Core statement coverage measured 63.1% in this development environment,
  below the release-quality spec's 80% target; `build.cmd coverage-gate`
  fails below that threshold and is a required release gate. `core/transport`
  (39.7%), `core/mobileapi` (55.7%), and `core/store` (56.0%) are the
  lowest-covered packages - see [the phase-8 delivery report](docs/phase-8-report.md)
  for detail.
- Revision rollback and portable/Evernote import recreate content as plain text; rich-text formatting is not round-tripped through either path (see [ASSUMPTIONS.md](ASSUMPTIONS.md)).
- The SQLCipher encrypted round trip passes on Windows amd64 and on an Android arm64 device through the packaged AAR and Flutter application linkage.
- The Go mobile binding, SQLCipher-linked Android AAR, and Flutter debug APK builds pass on Windows.
- Development packages are intentionally unsigned unless release signing is configured (`BERESTA_REQUIRE_SIGNING`, `BERESTA_SIGN_CERT_SHA1` for Windows Authenticode; `BERESTA_ANDROID_KEYSTORE_*` for Android release signing). `cmd/beresta-release-sign` publishes the signed desktop update manifest and can also produce a generic detached signature (for example, over a server release's `SHA256SUMS`); automatic update discovery/download by a running client and store packaging remain later-phase work. Server cross-builds additionally publish `SHA256SUMS`, a per-binary `go version -m` module manifest, and `provenance.json` (source commit, Go version, build timestamp) under `build/output/server/`.
- Desktop cold start, mobile cache/background behavior on physical hardware, and Raspberry Pi idle measurement require reference hardware this development environment does not have; their acceptance harnesses (`build.cmd cold-start`, Android instrumentation tests, `build/server/measure-idle-pi.sh`) are implemented and were not re-run as part of this change. The 20,000-note search budget and the 1,000-operation LAN sync budget were both re-verified in this environment and pass with headroom.

# Beresta

Beresta is an offline-first encrypted notes application for Windows and Android with an optional single-binary home synchronization server. Clients are complete local applications: the server is transport, not the authority for user data.

## Project Status

Beresta has completed phase 3 (complete local-only product core). Phases 1
and 2A delivered the architecture baseline and cryptographic core; phase 2B
added the encrypted client store; phase 3 adds the complete offline note
application surface on top of them, entirely through `core/account` — still
with no desktop or mobile screen wired to it yet:

- a buildable Wails v2 Windows host with a React/TypeScript frontend;
- a generated Flutter Android wrapper project with an analyzed and tested mobile shell;
- a Yjs V1/V2 adapter plus a SQLCipher 4.14 encrypted-database probe whose Android AAR is produced by `gomobile bind`;
- owned mutable secret buffers, device-bounded Argon2id, domain-separated HKDF, X25519/Ed25519 identities, XChaCha20-Poly1305 keybag/object/attachment/backup encryption, and the shared Windows DPAPI/Hello and Android Keystore/biometric key-wrapping contract (phase 2A);
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
- one root verification command and a Windows CI workflow.

Phase 4 (Windows desktop application) is in progress. The coarse Wails
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
to server" card that explains it is not available yet without blocking local
account creation, a returning-user unlock screen (chosen automatically when
a previous local account is on record), and a main shell with a keyboard-
accessible notebook tree, tag navigation (via a dedicated `SearchByTag`
binding that reuses the same search index as the search box, without its
text-query quoting limitations), and a virtualized note list
(`@tanstack/react-virtual`, since the account ceiling is 20,000 notes)
selecting into a working note editor. The editor is Quill 2 bound to the
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
and "Save as…" decrypts straight to a user-chosen destination. The note
list's search box (debounced ~150 ms after the last keystroke) composes
free text with tag/after/before/include-deleted filter controls into the
same `tag:`/`after:`/`before:`/`deleted:true` query language `SavedSearch`
stores verbatim, so saved searches round-trip through the same box; an
active search overrides the sidebar's notebook/tag browsing (cleared by
picking a notebook or tag, or by the box's own Clear button) and highlights
its matched free-text terms in each result's title. It runs through the
same `App.Search` → `core/account.Search` → `store.SearchNotes` path that
`core/store`'s 20,000-note / 150 ms budget test already benchmarks (see
above), so that budget covers this UI's queries too. A note's history
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
Evernote `.enex` import. Lock/unlock polish, the system tray/hotkey, and
the installer are the remaining phase-4 work (tasks 5.8 onward).

The completed phase-1 build matrix, test scope, review findings, and limitations
are recorded in [the phase-1 delivery report](docs/phase-1-report.md).
Cryptographic/platform protection verification is recorded in
[the phase-2A delivery report](docs/phase-2a-report.md), the encrypted
local store is recorded in
[the phase-2B delivery report](docs/phase-2b-report.md), and the complete
local-only product core is recorded in
[the phase-3 delivery report](docs/phase-3-report.md).

The current shell does not yet store real notes through a user interface or provide synchronization. Those capabilities are implemented and accepted in the ordered OpenSpec phases; do not use this revision for sensitive data.

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
build.cmd build
build.cmd package
build.cmd mobile-check
build.cmd mobile-bind-android
build.cmd mobile-build-android
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
| `build` | Build the React bundle and Windows Wails executable |
| `package` | Build and copy `beresta.exe` to ignored `build/output/` |
| `mobile-check` | Generate and validate the gomobile Java binding surface |
| `mobile-bind-android` | Produce `build/output/beresta-core.aar`; requires Android SDK/NDK |
| `mobile-build-android` | Produce the Android AAR and a Flutter debug APK with native SQLCipher linkage |
| `mobile-test-android` | Run the SQLCipher instrumentation round trip on a connected `arm64-v8a` Android device |
| `verify` | Run format checks, lint, tests, and package the phase-available artifacts |

The Windows executable is generated at `desktop/build/bin/beresta.exe` and copied to `build/output/beresta.exe`. Both locations are generated and ignored.

## Module-Specific Commands

### Go

```powershell
go test ./...
go vet ./...
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

Flutter Android packaging and native core binding commands are available at the repository root. `mobile-test-android` requires an online physical `arm64-v8a` device with USB debugging enabled and exercises both SQLCipher and the non-biometric Android Keystore wrapping path.

The current Yjs feasibility adapter accepts official Yjs V1 and V2 updates behind `core/sync/yjsadapter`; `core/mobileapi` exposes only gomobile-safe values. The SQLCipher feasibility adapter performs a transactional encrypted round trip, verifies reopen behavior, rejects a wrong key, and checks that neither the SQLite header nor a plaintext marker is visible in the database file. The Android AAR contains this CGo implementation for all supported ABIs, and the Flutter APK links it through Kotlin. The Android runtime gate is closed by `mobile-test-android` on arm64 hardware.

Local database keys use the versioned `BKW1` envelope from `core/keystore`, with key ID and purpose bound to the OS protection operation. Windows 11 build 22000 and later selects Windows Hello only when `UserConsentVerifier` is available; the owner-window prompt gates a user-scoped DPAPI unwrap. Windows 10 and systems without configured Hello expose the explicit DPAPI protection mode. Android stores non-exportable AES-256-GCM wrapping keys in Android Keystore; biometric mode is authentication-per-use through `BiometricPrompt.CryptoObject` and fails closed if strong biometrics are unavailable or the key is invalidated by enrollment changes.

English and Russian source strings live in `locales/en.json` and `locales/ru.json`. The root build validates both catalogs before linting and building, including explicit duplicate-key detection that ordinary JSON decoding does not provide.

## Configuration

The optional server will run with safe defaults and no configuration file:

```powershell
beresta-server --data ./data
```

Copy [config.example.yaml](config.example.yaml) to `config.yaml` only when changing defaults. `config.yaml` is intentionally ignored because it may contain local paths or deployment-specific certificate information. The server is implemented in phase 5; the example currently defines its reviewed configuration contract.

## Development Rules

- Keep user-visible strings in English/Russian localization catalogs.
- Keep code comments and configuration comments in English.
- Do not introduce external server infrastructure or abstractions for workloads above the documented five-user ceiling.
- Do not log content, passwords, keys, authorization tokens, invite codes, or arbitrary request/response objects.
- Update this README and `config.example.yaml` whenever a phase adds user-visible functionality or configuration.
- Every phase must compile, pass its available tests, complete a lead-level security/performance/UX review, and record known limitations.
- Architecture or security deviations require an ADR before implementation.

## Current Limitations

- The mobile application is still a phase-1 shell; the desktop application now has onboarding, unlock, a main shell (notebook tree, tags, note list), a working Quill/Yjs body editor, an attachment panel (drag-and-drop, clipboard image paste, inline image preview, save-as), a search box (instant filtered/full-text search, saved-query management, highlighted results), a note history panel (revision list, diff, restore), and a Backups & Data dialog (backup directory setting, catalog/preview/dry-run/restore, plaintext export, Beresta/Evernote import) wired to the real local-only product core, but has no lock/unlock polish, system tray/hotkey, or installer yet, and the complete phase-3 core is otherwise exercised only through Go tests. Opening an attachment with an external application is deliberately out of scope for now: doing so safely would require writing decrypted plaintext to a temp file, which conflicts with the "no plaintext attachment caches on disk" rule in `docs/threat-model.md`; "Save as…" (an explicit, user-directed export) and the inline in-memory preview cover the same need without that residue.
- `quill@2.0.3` (the current stable release) has an open low-severity XSS advisory in its HTML-export feature ([GHSA-v3m3-f69x-jf25](https://github.com/advisories/GHSA-v3m3-f69x-jf25)); Beresta never calls that feature (`getSemanticHTML`/HTML clipboard export), relying only on the Delta model and the Go core's own Markdown projection, so it is not reachable through anything this app does.
- The optional synchronization server has not been implemented; `core/transport`'s `SyncTransport` currently has only the default local no-op implementation.
- `core/account` and `core/store` measure 71.3% and 64.9% statement coverage respectively — below the phase-3 80% target. The gap is almost entirely defensive cleanup-on-error branches (for example, account creation's cascading `Close()` calls on each intermediate failure step); closing it needs dedicated fault-injection test infrastructure (mock wrappers/filesystems that fail on a specific call), comparable in effort to what phase 2B built for the blob store alone. See [the phase-3 delivery report](docs/phase-3-report.md) for detail.
- Revision rollback and portable/Evernote import recreate content as plain text; rich-text formatting is not round-tripped through either path (see [ASSUMPTIONS.md](ASSUMPTIONS.md)).
- The SQLCipher encrypted round trip passes on Windows amd64 and on an Android arm64 device through the packaged AAR and Flutter application linkage.
- The Go mobile binding, SQLCipher-linked Android AAR, and Flutter debug APK builds pass on Windows.
- Release signing, installers, automatic updates, and store packaging are implemented in later phases.

# Beresta

Beresta is an offline-first encrypted notes application for Windows and Android with an optional single-binary home synchronization server. Clients are complete local applications: the server is transport, not the authority for user data.

## Project Status

Beresta is in phase 2A (cryptographic core). The completed phase-1 architecture
and feasibility baseline plus the current cryptographic work provide:

- a buildable Wails v2 Windows host with a React/TypeScript frontend;
- a generated Flutter Android wrapper project with an analyzed and tested mobile shell;
- a Yjs V1/V2 adapter plus a SQLCipher 4.14 encrypted-database probe whose Android AAR is produced by `gomobile bind`;
- owned mutable secret buffers with copy-minimizing callback access and explicit wipe behavior on lock, close, and error paths;
- device-bounded Argon2id calibration and persisted KDF profiles with a fixed 128 MiB safety ceiling;
- profile-checked, domain-separated HKDF-SHA-256 derivation for keybags, workspace objects, private blob identities/chunks, backups, and pairing exports;
- X25519 account identities with libsodium-compatible anonymous workspace-key envelopes and domain-separated Ed25519 account/device signatures;
- XChaCha20-Poly1305 keybag and workspace-object encryption with deterministic CBOR associated data, random nonces, substitution resistance, and uniform keybag unlock failures;
- streaming workspace-private HMAC attachment IDs, encrypted manifests, independently authenticated 4 MiB chunks, nonce-reuse guards, and complete pre-publication verification;
- standalone XChaCha20-Poly1305 backup envelopes with complete authenticated headers and portable, traversal-safe SHA-256 file manifests;
- a shared versioned key-wrapping contract, user-scoped Windows DPAPI, Windows 11+ Hello-gated unwrap with an explicit DPAPI fallback, and Android Keystore AES-256-GCM with optional authentication-per-use biometrics;
- build-time validation for source English and Russian localization catalogs;
- the shared Go package structure used by later crypto, storage, backup, and synchronization phases;
- architecture, threat-model, crypto, synchronization, and ADR documentation;
- one root verification command and a Windows CI workflow.

The completed phase-1 build matrix, test scope, review findings, and limitations
are recorded in [the phase-1 delivery report](docs/phase-1-report.md).
Current cryptographic/platform protection verification is recorded in
[the phase-2A delivery report](docs/phase-2a-report.md).

The current shell does not yet store real notes or provide production encryption or synchronization. Those capabilities are implemented and accepted in the ordered OpenSpec phases; do not use this revision for sensitive data.

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

- The desktop and mobile applications are phase-1 shells, not functional note clients.
- The optional synchronization server has not been implemented.
- The SQLCipher encrypted round trip passes on Windows amd64 and on an Android arm64 device through the packaged AAR and Flutter application linkage.
- The Go mobile binding, SQLCipher-linked Android AAR, and Flutter debug APK builds pass on Windows.
- Release signing, installers, automatic updates, and store packaging are implemented in later phases.

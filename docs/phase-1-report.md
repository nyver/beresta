# Phase 1 Delivery Report

Date: 2026-08-17

## Scope

Phase 1 establishes the buildable architecture and feasibility baseline. It does
not implement the production notes domain, cryptographic key lifecycle,
synchronization server, or complete desktop/mobile user experience.

The delivered source includes:

- root Windows build orchestration, CI, ignored project-local tool/cache paths,
  and locked Go, npm, Flutter, Gradle, and Android inputs;
- the shared Go package boundaries under `core/`, the Wails/React desktop shell,
  the Flutter/Android shell, and the future server directory;
- architecture, STRIDE threat model, cryptographic profile, synchronization
  protocol, six ADRs, versioned schema documents, and compatibility fixtures;
- a bounded Yjs V1/V2 adapter and gomobile-safe facade;
- a SQLCipher 4.14.0 encrypted round-trip probe for Windows and Android;
- English/Russian source catalogs and strict catalog validation.

## Reference Environment

- Windows amd64
- Go 1.26.3
- Node.js 24.15.0 and npm 11.12.1
- Wails CLI 2.14.0
- Flutter 3.47.0 and Dart 3.13.0
- OpenJDK 21.0.4
- Android SDK API 36 and NDK 28.2.13676358
- Physical Android API 36 device with the `arm64-v8a` ABI

Generated phase artifacts:

- `build/output/beresta.exe`
- `build/output/beresta-core.aar`
- `mobile/build/app/outputs/flutter-apk/app-debug.apk`

All generated artifacts and project-local caches remain ignored by Git.

## Verification Matrix

| Command | Scope | Result |
|---|---|---|
| `build.cmd verify` | Go/Dart formatting checks, catalog validation, `go vet`, gomobile Java binding generation, TypeScript checking, Flutter analysis, Go/Vitest/Flutter tests, React production build, Wails Windows build, and Windows package copy | Pass |
| `build.cmd mobile-build-android` | Android AAR generation for supported ABIs and Flutter debug APK linkage | Pass |
| `build.cmd mobile-test-android` | Explicit install on one validated physical arm64 device and Android instrumentation of the SQLCipher round trip | Pass: 1 test |
| `go test -race ./...` with the documented project-local Go/CGo environment | Go concurrency and CGo tests, including concurrent Yjs adapter operations | Pass |
| `go mod verify` | Downloaded Go module checksum integrity | Pass |
| `go list -deps` review | Actually imported Go module graph and architecture boundary review | Pass with findings below |

The Go tests cover official Yjs V1/V2 update round trips, cross-format state,
malformed/oversized/lifecycle handling, concurrent adapter use, Windows
SQLCipher encryption/reopen/wrong-key/integrity behavior, plaintext inspection,
the gomobile facade, and catalog validation failures. Vitest covers the desktop
shell, Flutter tests cover the mobile shell, and Android instrumentation proves
that the packaged AAR is callable and SQLCipher-backed on arm64.

## Lead Review

The imported Go graph is intentionally small at this phase. First-party code
imports the pinned SQLCipher fork, `reearth/ygo`, Wails, WebView2 support, and a
small set of their runtime dependencies. SQLCipher remains behind `core/store`,
Yjs remains behind `core/sync/yjsadapter`, and neither dependency type crosses
the gomobile-facing API. No server or network package is active in the phase-1
product shells.

The review found and fixed these blocking issues:

1. The SQLCipher plaintext scanner retained the wrong bytes after its first
   read, allowing a marker split across that boundary to be missed. The buffer
   accounting is corrected and a boundary regression test is included.
2. Root builds could inherit implicit CGo selection and rely on stale cached
   test results. The build now enables CGo explicitly so the native SQLCipher
   implementation is always compiled or the command fails.
3. Android connected tests enumerated every adb target and could hang when an
   unrelated emulator was offline. The gate now selects one online arm64
   device, builds app/test APKs separately, installs with `adb -s`, and requires
   an explicit successful instrumentation result.
4. Kotlin compilation attempted to create daemon state in the user's local
   application-data directory. Kotlin now compiles in the Gradle process while
   all Gradle state stays under the ignored project-local cache.

No additional blocking correctness, race, secret-exposure, performance,
portability, or phase-appropriate UX finding remained after the fixes.

## Known Limitations

- The applications are feasibility shells and must not be used for sensitive
  notes. Production encryption, locking, storage, backup, synchronization, and
  server behavior begin in later phases.
- Android native runtime behavior was exercised on one physical arm64/API 36
  device. Other packaged Android ABIs received build/link coverage only.
- The Yjs corpus is deliberately small in this gate. Randomized convergence and
  differential tests against JavaScript Yjs are later-phase acceptance work.
- The Flutter 3.47 template currently emits Android Gradle Plugin warnings for
  transitional legacy Kotlin/DSL flags. They do not fail this pinned build but
  must be removed during a tested built-in-Kotlin migration before AGP 10.
- Phase 1 has no production-scale startup, search, synchronization, mobile
  background, or Raspberry Pi benchmark because those implementations do not
  exist yet.
- Vulnerability and OSV release gates, signing, installers, SBOM/provenance, and
  clean-machine release builds are explicitly scheduled in later OpenSpec
  tasks; module integrity and the imported graph were reviewed here, but this
  phase report is not a substitute for those release scans.

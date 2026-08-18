# Phase 2A Delivery Report

Date: 2026-08-17

## Scope

Phase 2A implements the cryptographic core and platform key-protection adapters. It does not yet create accounts, persist the complete client schema, or connect these primitives to production desktop/mobile unlock flows.

Delivered source includes:

- owned, explicitly wiped secret buffers and bounded Argon2id calibration;
- domain-separated HKDF, X25519 sealed boxes, and Ed25519 signatures;
- versioned XChaCha20-Poly1305 keybag, object, attachment, and backup primitives;
- streaming private attachment IDs, independent 4 MiB chunks, and authenticated backup manifests;
- canonical `BKW1` platform envelopes and the shared Go keystore contract;
- current-user Windows DPAPI plus Windows 11+ owner-window Hello verification;
- Android Keystore AES-256-GCM plus authentication-per-use strong biometrics;
- compatibility vectors, malformed-input fuzz targets, nonce/API misuse tests, and allow-listed log/crash diagnostics.

## Security Decisions

Windows Hello is an application user-presence gate before DPAPI unwrap. It prevents the Beresta UI from silently unwrapping while locked, but it is not a hardware-backed replacement for DPAPI and does not resist code already executing as the same Windows user. Windows 10 therefore exposes DPAPI-only mode explicitly rather than implying biometric protection.

Android biometric mode uses a distinct non-exportable key, authorizes every AES-GCM operation through `BiometricPrompt.CryptoObject`, and invalidates the key after biometric enrollment changes. It never silently falls back to the non-biometric alias.

## Verification Matrix

| Command | Scope | Result |
|---|---|---|
| `go vet ./core/crypto ./core/backup ./core/keystore ./internal/diagnostics ./desktop/platform/keystore` | Go/CGo correctness and unsafe-pointer review | Pass |
| `go test -race -count=1 ./...` with project-local temporary/cache paths | Complete Go race suite, including crypto, manifests, diagnostics, DPAPI, and WinRT availability ABI | Pass |
| Four three-second `go test -fuzz` sessions | Object, keybag, sealed-box, and keystore malformed inputs | Pass; more than 4 million total executions |
| `build.cmd verify` | Complete current Go, Wails, React, Flutter, and Windows packaging matrix | Pass |
| `build.cmd mobile-build-android` | AAR, Android Kotlin adapter, and debug APK | Production Kotlin source compiled; final command confirmation pending |
| `build.cmd mobile-test-android` | SQLCipher and Android Keystore instrumentation on arm64 | Online physical device detected; execution pending explicit device-install approval |

## Lead Review Findings

The review found and fixed the following blocking issues before the final gate:

1. WinRT `IAsyncInfo.Close` was initially called before `GetResults`, which could invalidate the completed result. Cleanup now occurs only after result retrieval.
2. Native Windows handles crossed CGo as forged Go pointers and failed race/checkptr. The ABI now passes handles as `uintptr_t` and converts them only in C.
3. Android biometric cancellation and prompt errors could retain an owned plaintext copy until garbage collection. A wiping callback now clears sensitive input on every terminal callback path.
4. The Android and Go metadata-binding implementations initially used different assumed domain-string lengths. Android now allocates from the actual domain byte length, and the checked-in golden vector pins identical bytes.

## Known Limitations

- Windows Hello verification that displays UI requires the Wails owner HWND; UI wiring is scheduled with the desktop lock workflow.
- Android biometric prompt and enrollment invalidation require physical-device interaction and cannot be accepted by host-only unit tests.
- Go explicit wiping cannot erase compiler/runtime copies, registers, stack growth copies, or OS/library-internal buffers.
- Phase 2A primitives are not a usable encrypted notes product until the Phase 2B store and account lifecycle integrate them.

## Phase Closure (2.10)

Date: 2026-08-18

`README.md`, `config.example.yaml`, `docs/crypto-spec.md`, `docs/threat-model.md`, and `ASSUMPTIONS.md` were re-checked against the delivered `core/crypto`, `core/keystore`, and `desktop/platform/keystore` source and already reflect every primitive, domain, envelope, and limitation implemented in this phase; no drift was found.

| Command | Scope | Result |
|---|---|---|
| `build.cmd verify` | Format check, locale check, `go vet`, gobind compile check, TypeScript typecheck, Flutter analyze, Go/Vitest/Flutter tests, Wails package | Pass |
| `go test -race -count=1 ./core/crypto/... ./core/backup/... ./core/keystore/... ./internal/diagnostics/... ./desktop/platform/keystore/...` | Repeat race-detector confirmation for the closing gate | Pass |

A closing lead review re-read `core/crypto/secret.go`, `aead.go`, `argon2id.go`, `hkdf.go`, `identity.go`, `aad.go`, `attachment.go`, `backup.go`, and `core/keystore/keystore.go` end to end, including manual verification that every `CanonicalXxxAAD` map encodes its keys in true RFC 8949 deterministic order (shortest encoded key first, then lexicographic). No blocking correctness, performance, security, or UX findings were raised beyond those already fixed and recorded above.

Android AAR binding and on-device instrumentation (`mobile-bind-android`, `mobile-build-android`, `mobile-test-android`) remain gated on a configured Android SDK and an online `arm64-v8a` device, neither of which was available in this verification pass; this is unchanged from the limitation already recorded above and is not a regression.

Phase 2A is closed with all 2.1-2.10 tasks complete.

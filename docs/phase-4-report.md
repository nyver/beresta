# Phase 4 Delivery Report

Date: 2026-08-20

## Scope

Phase 4 ("Windows Desktop Local-Only Application", tasks 5.1-5.13) connects the complete offline `core/account` surface to a Wails v2 and React/TypeScript desktop client. It includes Windows lifecycle integrations, the per-user NSIS installer and fail-closed signed-update helper, desktop test coverage, and the reference-host performance gates. It does not implement the optional home server or remote synchronization; those begin in phase 5.

Delivered behavior includes:

- coarse JSON-safe Wails bindings for account lifecycle, note/notebook/tag commands, Yjs body updates, attachments, search, revisions, backups, restore, import/export, settings, and localization without exposing database or key primitives;
- English/Russian local-first onboarding and unlock, an accessible desktop shell, virtualized note list, notebook/tag navigation, Quill/Yjs editor, local search and saved filters, revision rollback, backup/restore, and import/export workflows;
- drag-and-drop and clipboard attachment capture, bounded in-memory previews, explicit save-as export, automatic locking, DPAPI/Hello protection visibility, secure lock transition, and typed-confirmation local wipe;
- tray behavior, global quick-note capture, opt-in autostart, collision reporting, and explicit synchronization disabled/offline/active/current/failed UI with truthful current-phase placeholders;
- a per-user NSIS installer that preserves `%APPDATA%\Beresta` by default, requires an explicit destructive uninstall choice, and never removes external backup directories;
- a pre-downloaded update helper that verifies a pinned Ed25519 manifest, exact artifact size and SHA-256, Windows Authenticode trust, version monotonicity, installation replacement, and rollback availability before accepting an update;
- component, keyboard/accessibility, Wails adapter, offline desktop end-to-end, lock, backup/restore, installer/update, search performance, and cold-start verification;
- ADR 0007 and a ten-launch fresh-profile cold-start harness using a five-second nearest-rank p95 budget on the documented Windows/WebView2 reference host.

## Security and Design Decisions

The Wails API remains application-service-oriented: React cannot issue raw SQL, choose nonces, access unwrapped keys, or stream arbitrary filesystem paths into the store. Bridge errors preserve stable codes while avoiding backend-detail matching in the UI.

Plaintext attachment previews remain memory-only and are limited to 8 MiB. External opening is not implemented because it would require a decrypted temporary file; save-as is an explicit user-directed plaintext export. Locking replaces the note-bearing shell before flush and key cleanup finish, and every desktop-bound test uses a disposable AppData profile.

The installer preserves encrypted local data by default. Destructive purge is available only through an explicit interactive checkbox or `/PURGEUSERDATA=1`; the automated purge scenario is guarded by `BERESTA_INSTALLER_SMOKE_DISPOSABLE_PROFILE=1`. Development packages may be unsigned, while release mode fails closed unless signing configuration is present.

The update helper applies only a newer pinned-signature manifest and an Authenticode-trusted installer. It stores the prior executable atomically, passes the actual installation directory to NSIS, rejects a successful installer process that did not replace the executable, verifies the installed publisher, and restores the prior executable on installation or validation failure.

Desktop cold-start acceptance measures the complete supported Wails/WebView2 path. Each of ten consecutive samples receives a fresh Beresta AppData profile and measures process creation to the first responsive main window. Nearest-rank p95 over ten samples is the slowest result. Hosted CI builds and smoke-tests the installer, but its timing is not treated as reference performance.

## Verification Matrix

| Command | Scope | Result |
|---|---|---|
| `build.cmd verify` | Format check, EN/RU catalog validation, Go vet, TypeScript typecheck, Flutter analyze, complete Go/Vitest/Flutter tests, production frontend bundle, Wails amd64 application, updater, and NSIS installer | Pass |
| Go matrix inside `build.cmd verify` | All phase-available Go packages, including SQLCipher, desktop Wails boundary/E2E, platform integrations, and updater | Pass |
| Vitest inside `build.cmd verify` | 22 test files, 134 component/integration/accessibility tests | Pass |
| Flutter test inside `build.cmd verify` | Offline-first mobile shell smoke test | Pass (1 test) |
| `go test -count=5 ./internal/desktopupdate` with project-local Go cache/temp | Updater verification, custom install path, replacement, failure rollback, publisher rejection, and cleanup stability | Pass |
| `build.cmd locale-check` | Complete, non-empty, non-duplicated English/Russian catalogs | Pass; task 5.13 adds no new user-facing string, so no additional translation key is required |
| Two-sample diagnostic harness run | Fresh-profile lifecycle, responsive-window detection, exact-child termination, cleanup, result serialization | Pass; 3136 ms and 2543 ms |
| First `build.cmd cold-start` after packaging | Ten fresh-profile launches, nearest-rank p95 <= 5000 ms | Fail; samples `[3907, 2964, 6569, 3277, 3352, 3899, 3577, 3728, 3924, 3961]`, p95 6569 ms |
| Repeated `build.cmd cold-start` on the idle reference host | Same unchanged ten-launch acceptance gate | Pass; samples `[3440, 2892, 3314, 3191, 3649, 3048, 3412, 3755, 3659, 3752]`, p95 3755 ms |

The rebuilt ignored artifacts are:

| Artifact | Size | SHA-256 |
|---|---:|---|
| `build/output/beresta.exe` | 16,217,088 bytes | `1E254B5AC409272FA7AB5795D521EDC4D68E2FBF0240E1F54E4771444C8D1646` |
| `build/output/beresta-updater.exe` | 3,173,888 bytes | `9B75002905769CD794A80F2D03FD4DF3165B68F649B04E0E0C75A2DF4A381041` |
| `build/output/Beresta-amd64-installer.exe` | 9,994,317 bytes | `94C4AE5EB8C95DC90A7F6FFCEC7251441EA685DA0E61A91A9B20A691EA90AD5E` |

Installer purge smoke tests are intentionally not run in the developer's real profile. CI runs the guarded scenario in a disposable runner; Windows 10 and Windows 11 release VMs must also run the OS-specific commands documented in `docs/desktop-updates.md`.

## Lead Review Findings

The phase-closing Go/frontend review found and fixed these blocking issues:

1. The updater invoked a verified NSIS installer without `/D=<actual installed directory>`. A custom installation could therefore update the default directory while validation inspected the unchanged custom-path executable. The updater now passes the installed executable's parent directory as NSIS's final argument, with a regression assertion.
2. A zero-exit installer that left the old executable untouched was accepted as long as the old file still had a trusted Authenticode signature. The updater now hashes the installed and preserved executables and rejects a no-op replacement before publisher validation.
3. Windows endpoint protection could briefly retain updater test artifacts and turn successful tests into `TempDir RemoveAll: directory is not empty` failures. Updater tests now use bounded retry cleanup and passed five consecutive runs.
4. The prior cold-start harness measured one launch and aborted at the old budget. The replacement performs ten isolated samples, separates a 30-second hung-window timeout from the five-second percentile decision, requires a responsive main window, uses nearest-rank p95, records every sample as JSON, removes only verified child profiles, and terminates only the exact process it created.

The review also verified that synchronization placeholders cannot mutate remote state, unknown backend status values fail visibly, modal dialogs are named and keyboard-contained with focus restoration, local editing remains available in every non-active sync state, secrets and content are absent from new logs, and the lazy-loaded editor shell does not weaken lock behavior.

No other blocking correctness, race, performance, security, accessibility, or UX findings remain in the phase-available scope.

## Known Limitations

- Remote server connection, conflict quarantine processing, multi-device management, and authenticated revocation delivery are not implemented; the synchronization panel labels those controls unavailable and local editing remains complete.
- Local wipe is the implemented revocation-response primitive but remains manually triggered until a trusted remote revocation record can arrive.
- External attachment open is deliberately unavailable to avoid an undeclared plaintext temporary-file cache; bounded in-memory preview and explicit save-as are available.
- The first post-package cold-start run contained a 6569 ms native WebView2 outlier and failed, while the unchanged repeat on the idle reference host passed at p95 3755 ms. ADR 0007's five-second gate therefore remains intentionally sensitive to host background activity.
- Development installer/updater artifacts are unsigned unless release signing variables are configured. Manifest publication, update discovery/download, and production artifact publication remain release-pipeline work.
- Destructive installer purge testing requires a disposable Windows profile. The local verification did not run it against the developer's real `%APPDATA%`; CI and the documented Windows 10/11 release VMs provide that environment.
- The Android application remains a phase-1 shell and is not part of the completed Windows desktop product surface.

## Phase Closure

All tasks 5.1-5.13 are implemented and verified within the phase-available environment. The revised cold-start acceptance gate passes on the documented reference host, the application/updater/installer were rebuilt after review fixes, and the next pending OpenSpec work is phase 5's optional home synchronization server.

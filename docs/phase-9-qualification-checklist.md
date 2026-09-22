# Phase 9 Physical Qualification Checklist

Tasks 12.4 (Windows) and 12.5 (Android) require running Beresta on real
displays and real devices to produce data this repository's automated
suites cannot: DPI scaling correctness, actual screen-reader behavior,
real sleep/resume timing, and physical biometric hardware. No development
sandbox can substitute for this - see
[the phase-9 delivery report](phase-9-report.md)'s "Known limitations" for
why. This checklist is the runbook for whoever has that hardware: each
item lists the harness already built to support it (if any) and exactly
what to record.

Record results as one row per item in a copy of this file (or an issue/
spreadsheet), with: pass/fail, host/device identity, OS build, and the
date. A release is not accepted until every item below is either recorded
passing or has a documented, accepted exception.

## 12.4: Windows 10/11 qualification

| Item | Harness / entry point | What to record |
| --- | --- | --- |
| 100% DPI | Manual: launch, exercise onboarding, shell, editor, all dialogs | No clipped/overlapping text or controls |
| 125% DPI | Manual, same pass | Same |
| 150% DPI | Manual, same pass | Same |
| Keyboard-only operation | Manual: unplug the mouse, drive the full app via `desktop/frontend/src/shell/commands.ts`'s registry (Tab, the shortcuts it documents, Escape) | Every control reachable, focus never lost or trapped outside a dialog |
| Screen reader | Manual with Narrator (built-in) and/or NVDA | Every control announces a name and role; no silent/unlabeled controls |
| Sleep/resume | Manual: sleep the host mid-edit with unsaved content, resume | Content intact, no duplicate/lost commit, sync resumes correctly |
| Windows Hello on | Manual: enroll Hello, attempt unlock | Expected: DPAPI is used regardless (Hello was removed - see `docs/desktop-updates.md` and task 7.4's finding); confirm no crash and passphrase unlock still works |
| Windows Hello off | Manual: no Hello enrolled | Passphrase unlock works |
| Installer | `powershell build/windows/smoke-installer.ps1 -InstallerPath build/output/Beresta-amd64-installer.exe -ExpectedOS Windows10` and again with `-ExpectedOS Windows11` | Both OS-specific runs pass (`docs/desktop-updates.md`) |
| Update | Manual: stage an older build, run through `beresta-updater apply`, confirm the new version launches with data intact | Update completes, prior version preserved as `.previous` |
| Rollback | Manual: force an update failure (e.g. a corrupted staged artifact) | `beresta-updater rollback` restores the prior executable; automated coverage already proves the underlying logic (`internal/desktopupdate`'s rollback-failure tests, task 8.7) - this step confirms the real installer path end to end |
| Uninstall | Manual: interactive uninstall (preserve and purge paths), then silent `/PURGEUSERDATA=0` and `=1` | Data preserved/purged exactly as chosen |
| Cold start | `build.cmd cold-start` (ten-sample, nearest-rank p95 budget: 5s) | Pass/fail and the measured p95, per [ADR 0007](adr/0007-desktop-cold-start-budget.md) |

## 12.5: Android qualification

| Item | Harness / entry point | What to record |
| --- | --- | --- |
| Current Android version | `build.cmd mobile-test-android` (SQLCipher/Keystore/secure-window/capture/background-work instrumentation) on a device running the current stable Android release | Pass/fail |
| Current-minus-two version | Same, two major Android versions back (the task's named floor) | Pass/fail |
| Low-end hardware | Same instrumentation suite on a low-RAM/low-CPU reference device | Pass/fail, note any timeout adjustment needed |
| Mid-range hardware | Same, mid-tier device | Pass/fail |
| Biometric on | Manual: enroll biometrics, unlock via `unlockWithDeviceAuthentication` | Succeeds; cancellation is non-error navigation (task 7.5) |
| Biometric off | Manual: no biometric enrolled, device credential (PIN) configured | Falls back to device credential; passphrase remains available |
| Battery/background limits | Manual: enable aggressive battery optimization for the app, background it, wait past the WorkManager periodic interval | Foreground reopen still resumes pending sync (task 3.4's actual durability contract - background WorkManager triggers are best-effort, not the mechanism of record; task 9.4's documented `BIOMETRIC`-secret gap means a fully-backgrounded periodic sync may not fire at all, which is expected, not a bug) |
| Process recreation | Manual: enable "Don't keep activities" in Developer Options, background the app mid-edit, return | Editor session starts clean and reads durably saved content (task 2.3/2.5) |
| SAF local provider | Manual: back up to and restore from a local Storage Access Framework destination | Backup/restore round-trips |
| SAF cloud provider | Manual: same, to a cloud-backed SAF provider (Google Drive, etc.) | Backup/restore round-trips; note provider-specific latency |
| Normal font scale | `flutter test` already covers default scale; manually confirm on-device | No regression vs. the automated suite |
| Large font scale | Manual: set the device to its largest font scale | No clipped controls/overflow (tasks 9.3/9.7 already fixed known cases via automated large-text-scale regression tests; this step confirms on real hardware) |
| TalkBack | Manual: enable TalkBack, navigate the full app | Every control has a meaningful label and logical traversal order (task 9.7) |
| Editor frame/jank | `flutter test integration_test/editor_jank_test.dart -d <device>` | Genuine frame-build timing while sync/backup/data-check work is held pending (task 11.3); record the `traceAction` summary |

## Notes for whoever runs this

- Every automated harness referenced above already exists and passes in
  CI or this repository's own test suites; nothing here requires new code,
  only hardware access this authoring environment did not have.
- If an item fails, file it against the relevant task in
  `openspec/changes/harden-product-ux-reliability/` (or its successor once
  archived) rather than silently working around it - these are release
  gates, not suggestions.

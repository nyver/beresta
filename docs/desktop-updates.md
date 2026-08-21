# Windows Installer and Updates

## Package layout

`build.cmd package` creates these ignored artifacts under `build/output/`:

- `beresta.exe` — the Wails desktop application;
- `beresta-updater.exe` — the update verifier and rollback helper;
- `Beresta-amd64-installer.exe` — the per-user NSIS installer.

NSIS 3.12 must be available as `makensis.exe`. The build discovers a normal
NSIS installation and the project-local portable path
`build/tools/nsis-3.12/nsis-3.12/makensis.exe`.

The installer preserves `%APPDATA%\Beresta` by default. Interactive uninstall
requires the user to select a destructive checkbox before deleting that
directory. Silent uninstall accepts `/PURGEUSERDATA=0` (preserve) or
`/PURGEUSERDATA=1` (delete). External backup directories are never removed.

## Release signing

Development packages may be unsigned. A release environment must set:

- `BERESTA_REQUIRE_SIGNING=1` to reject unsigned output;
- `BERESTA_SIGN_CERT_SHA1` to the 40-character SHA-1 thumbprint of the
  Authenticode certificate in the Windows certificate store;
- `BERESTA_UPDATE_PUBLIC_KEY_BASE64` to the base64-encoded 32-byte Ed25519
  public key pinned into `beresta-updater.exe`;
- optionally, `BERESTA_SIGNTOOL` to an explicit `signtool.exe` path.

The private Ed25519 release key and code-signing private key must remain outside
the repository and CI logs. Authenticode-sign the application, updater,
uninstaller, and final installer with `signtool.exe` (SHA-256 plus an RFC 3161
timestamp) using the certificate identified by `BERESTA_SIGN_CERT_SHA1`.

The pre-downloaded update manifest follows
[`schema/update-manifest-v1.schema.json`](../schema/update-manifest-v1.schema.json).
Its Ed25519 signature covers deterministic compact JSON containing the fields
in this order: `format_version`, `version`, `platform`, `artifact`,
`size_bytes`, and lowercase `sha256`. The `signature` field itself is excluded.

`cmd/beresta-release-sign` publishes that manifest for one already-built,
already-Authenticode-signed artifact:

```powershell
$env:BERESTA_RELEASE_PRIVATE_KEY_BASE64 = "<base64 64-byte Ed25519 private key>"
go run ./cmd/beresta-release-sign -artifact build/output/Beresta-amd64-installer.exe -version 1.2.0
```

It hashes the artifact, builds and signs the canonical payload, writes
`<artifact>.manifest.json`, and re-verifies its own output with the same
strict decoder `beresta-updater` uses before printing the paired public key to
pin into `BERESTA_UPDATE_PUBLIC_KEY_BASE64`. Automatic discovery/download of a
published manifest by a running client belongs to a future phase; the current
updater only applies a manifest and artifact already staged on disk (see
`beresta-updater apply -manifest <path> -installed <path>`), and it
deliberately fails closed without a pinned public key.

Before replacing the installed executable, the updater verifies the manifest
signature, exact version policy, artifact basename, byte length, SHA-256 hash,
and Windows Authenticode trust. It retains `beresta.exe.previous`, invokes the
installer silently, verifies the installed executable's Authenticode trust,
and restores the prior executable if installation or post-install validation
fails. `beresta-updater rollback -installed <path>` restores that retained
copy explicitly.

## Windows smoke tests

The cold-start gate launches the packaged application ten consecutive times,
uses a fresh Beresta AppData profile for every sample, waits for the first
responsive main window, and enforces a nearest-rank p95 budget of five seconds.
With ten samples, p95 is the slowest launch. The harness terminates only the
exact application process it created and writes the complete measurements to
`build/output/cold-start/last-run.json`:

```powershell
build.cmd cold-start
```

Run this measurement with the supported WebView2 runtime already installed on
the reference machine documented in
[`ADR 0007`](adr/0007-desktop-cold-start-budget.md); hosted CI timing is not
treated as reference performance. `-SampleCount` may shorten a diagnostic run,
but only the default ten-sample command is release acceptance. The 20,000-note
local-search budget is a separate deterministic test in
`core/store/search_bench_test.go`.

Installer smoke tests delete `%APPDATA%\Beresta` during their explicit purge
case. Run them only in an isolated, disposable Windows user profile or CI VM:

```powershell
$env:BERESTA_INSTALLER_SMOKE_DISPOSABLE_PROFILE = "1"
build.cmd installer-smoke
```

The scenario verifies initial installation, update-time preservation of the
prior executable, default local-data retention, and explicit local-data purge.
For the release matrix, run the same packaged installer separately on Windows
10 and Windows 11 and enforce the expected host version:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File build/windows/smoke-installer.ps1 `
  -InstallerPath build/output/Beresta-amd64-installer.exe -ExpectedOS Windows10

powershell -NoProfile -ExecutionPolicy Bypass -File build/windows/smoke-installer.ps1 `
  -InstallerPath build/output/Beresta-amd64-installer.exe -ExpectedOS Windows11
```

Set `BERESTA_INSTALLER_SMOKE_DISPOSABLE_PROFILE=1` in each VM before running
those commands. A release is not accepted until both OS-specific runs pass.

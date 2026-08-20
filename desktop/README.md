# Beresta desktop

The Windows desktop client uses Wails v2 with a React and TypeScript frontend.
Run the documented bootstrap command after installing the required Go, Wails, and Node.js toolchain.

`build.cmd package` creates the application, fail-closed update helper, and
per-user NSIS installer. Uninstall preserves encrypted local data by default;
deleting it requires an explicit checkbox or `/PURGEUSERDATA=1`. Release
signing and disposable Windows 10/11 smoke-test details are documented in
[`docs/desktop-updates.md`](../docs/desktop-updates.md).

`build.cmd cold-start` runs the release cold-start gate: ten consecutive
fresh-profile launches, measured from process creation to the first responsive
main window, with a five-second nearest-rank p95 budget. The reference host and
rationale are recorded in
[`ADR 0007`](../docs/adr/0007-desktop-cold-start-budget.md).

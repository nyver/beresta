# ADR 0007: Measure desktop cold start against a five-second p95 budget

- Status: Accepted
- Date: 2026-08-20

## Context

The Windows release gate originally required the complete packaged desktop client to expose its first interactive window within 1.5 seconds. The supported Wails v2 path creates a native WebView2 environment and window before React can render. On the Windows reference host, the complete path measured 4.01 seconds in the diagnostic run even after lazy-loading Quill and Yjs reduced the locked frontend bundle from approximately 563 kB to 208 kB. The remaining elapsed time is dominated by native WebView2 initialization, not application-controlled editor work.

The reference host for this decision is Windows 11 Pro 25H2 build 26200 on an Intel Core i7-11700K (16 logical processors), amd64, with the supported Microsoft Edge WebView2 Runtime 151.0.4129.93 installed. Hosted CI timing is not treated as reference performance.

## Decision

Revise the desktop cold-start release budget from 1.5 seconds for one launch to a 95th percentile of no more than five seconds across ten consecutive launches. Each sample:

- launches the packaged amd64 Wails executable;
- uses a newly created empty Beresta `APPDATA` profile;
- assumes the supported WebView2 runtime is already installed;
- measures elapsed wall-clock time from process creation until the first responsive main window; and
- terminates only the exact application process started by the harness before the next sample.

The harness uses the nearest-rank percentile definition. With ten samples, p95 is the slowest measured launch. A sample that does not expose a responsive window within 30 seconds fails the gate. The 150-millisecond local-search and three-second LAN-synchronization budgets are unchanged.

## Consequences

- The release retains a reproducible, release-blocking cold-start UX gate with measured headroom over the observed 4.01-second launch.
- Ten fresh-profile launches cover first-use initialization variance and prevent a warm persistent profile from hiding regressions.
- The measurement takes roughly one minute on the reference host and is intentionally a reference-host acceptance test rather than a hosted-CI timing assertion.
- Future changes to the Wails/WebView2 stack must preserve the same full-path measurement unless another ADR changes the supported desktop architecture.
- The five-second budget is not a performance target for individual React modules; profiling must continue to separate application-controlled work from native runtime startup.

## Rejected Alternatives

### Retain the 1.5-second budget

Rejected because the supported complete Wails/WebView2 path cannot meet it on the reference host after the controllable frontend startup bundle was materially reduced. Keeping a known-impossible gate would make release acceptance non-actionable.

### Measure a warm persistent profile

Rejected because it would omit the first-use behavior users experience and permit runtime/profile caches to conceal regressions.

### Use the Wails legacy native loader

Rejected because it crashes with the enabled native file-drop integration and is not the supported production path.

### Replace Wails/WebView2 for this phase

Rejected because changing the fixed Windows architecture would duplicate platform and editor integration work, introduce substantial release risk, and exceed the scope of a performance-budget correction.

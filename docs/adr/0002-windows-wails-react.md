# ADR 0002: Use Wails v2 with React and TypeScript for Windows

- Status: Accepted
- Date: 2026-08-16

## Context

The Windows 10/11 client needs a native desktop process, rich Markdown/WYSIWYG editing, drag-and-drop, clipboard images, global hotkeys, tray integration, Windows Hello, autostart, installers, and signed updates. The shared core is Go.

## Decision

Use Wails v2 as the Windows process and binding layer, with React and TypeScript rendered by WebView2. The Go host exposes coarse application services and state events; it never exposes raw SQL, filesystem, nonce, or key operations.

Use Vite for deterministic frontend builds. Keep generated Wails bindings at the adapter boundary. React exchanges incremental Yjs-compatible updates with the Go service, but the Go core owns canonical persistence, encryption, synchronization, and backups.

Package the application as one Beresta executable plus an NSIS installer. Treat WebView2 as a documented Windows runtime prerequisite and verify signed updates before replacement.

## Consequences

- Go services are reused directly without a local RPC daemon.
- The React editor ecosystem is available for rich text and Yjs integration.
- WebView2 lifecycle, generated bindings, and UI asset embedding become release inputs that require tests.
- Windows OS features need small Go/platform adapters and cannot be implemented purely in React.
- Frontend success never implies persistence success; Wails calls must return explicit committed-state results.

## Rejected Alternatives

### Electron

Rejected because it duplicates a browser runtime, increases memory and package size, and adds a Node process without improving access to the existing Go core.

### Native WinUI/WPF

Rejected because it introduces a second application language and binding model and offers less direct reuse of the selected rich-text/Yjs web ecosystem.

### Flutter desktop

Rejected because the fixed architecture requires Wails for Windows and because the shared React editor stack is better aligned with Yjs rich-text integration.

### Browser-only client

Rejected because it cannot meet local SQLCipher, OS-keystore, global hotkey, tray, filesystem, and full offline requirements without a separate native service.

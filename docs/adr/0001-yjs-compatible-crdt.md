# ADR 0001: Use a Yjs-Compatible CRDT Through `reearth/ygo`

- Status: Accepted
- Date: 2026-08-16

## Context

Beresta must merge concurrent offline edits to a rich note body without conflict copies or lost paragraphs. The CRDT runs in the shared Go core on Windows and Android, while the React editor ecosystem benefits from Yjs interoperability. The server cannot decrypt or merge content.

The selected implementation must work through `gomobile bind`, avoid a JavaScript runtime in the core, preserve a versioned update format, and support differential convergence testing against an independent implementation.

## Decision

Use the Yjs document and update model behind a small Beresta-owned `DocumentCRDT` interface. Implement that interface with the pure-Go `github.com/reearth/ygo` module, pinned to v1.48.0 for the phase-1 feasibility gate.

Pin a reviewed release or commit. Store Yjs V1/V2 compatibility fixtures under `schema/testdata`, run byte-level interoperability tests against official JavaScript Yjs, and run randomized convergence tests across multiple Go replicas. Beresta stores opaque Yjs updates and state vectors but does not expose `ygo` types outside the adapter.

Note bodies use Y.Text/Y.Xml-compatible rich-text state. Titles, notebook placement, tags, and flags remain separate HLC/LWW metadata registers. Canonical Markdown is a derived local projection for FTS, export, diff, and preview, not the merge source.

## Consequences

- The same CRDT implementation runs in desktop and mobile core builds without cgo or embedded JavaScript.
- React editor bindings can exchange standard Yjs updates with the Go core.
- Persisted updates remain interoperable beyond one editor component.
- A library defect could affect every client, so the adapter, pinned dependency, differential fixtures, and property tests are mandatory.
- Yjs history can grow; encrypted client snapshots and acknowledged compaction bound replay cost without trusting the server to merge.

## Rejected Alternatives

### Automerge through native bindings

Rejected because the available Go integration adds Rust/C or FFI complexity to Windows and `gomobile` builds. It also provides less direct interoperability with the selected React rich-text ecosystem.

### Run JavaScript Yjs inside every client core

Rejected because Flutter would need an additional JavaScript runtime and platform bridge, splitting ownership of the core state machine and complicating background execution.

### Implement a new CRDT

Rejected because convergence algorithms and wire compatibility are security- and correctness-sensitive. Beresta must not invent one.

### Operational transform with a central server

Rejected because it requires a trusted available content-aware coordinator and conflicts with offline-first E2EE behavior.

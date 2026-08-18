# ADR 0003: Use Flutter with `gomobile bind` for Android

- Status: Accepted
- Date: 2026-08-16

## Context

The Android client requires offline editing, platform keystore and biometrics, background synchronization, recent-apps privacy, QR pairing, share-target capture, app widgets, backup destinations, and configurable attachment caching. Cryptography, storage semantics, backup, and synchronization must remain in the shared Go core.

## Decision

Use Flutter for the Android UI and lifecycle-independent presentation logic. Produce an Android AAR from a narrow `core/mobileapi` package with `gomobile bind`. Pin the phase-1 binding tool to `golang.org/x/mobile` revision `1960c775504c`.

The bound API uses primitive values and serialized bounded request/response structures. It avoids Go callbacks from arbitrary threads; Flutter consumes events through polling or a platform-safe stream bridge. Android adapters provide Keystore, biometrics, WorkManager, secure-screen flags, document providers, share-intent handoff, app widgets, and connectivity signals.

The Go core owns validated commands, encryption, local repository semantics, CRDT state, synchronization, and backups. Flutter never receives long-lived private keys or database handles.

## Consequences

- Flutter provides one Android UI stack while retaining one security-sensitive Go core shared with Windows.
- AAR packaging, SQLCipher native linkage, and platform channels require explicit reproducible build gates.
- Background work must be bounded and resumable because mobile operating systems can suspend it.
- Android share targets and app widgets need minimal native host code and encrypted handoff containers.

## Rejected Alternatives

### Reimplement the core in Kotlin

Rejected because it duplicates cryptography, migrations, sync, and recovery logic and creates compatibility risk with the Windows client.

### React Native

Rejected because the fixed architecture selects Flutter and because bridging the shared Go core plus native security/background APIs would not reduce platform-specific work.

### Flutter reimplementation of the core

Rejected because it violates the shared-Go-core constraint and duplicates the most security-sensitive behavior.

### Persistent local Go server consumed over HTTP

Rejected because mobile lifecycle restrictions make a background daemon unreliable and unnecessarily expose an additional local network/API boundary.

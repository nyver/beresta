# Phase 7 delivery report

## Delivered scope

Phase 7 turns the generated Android shell into a local-first application using
the same Go account, SQLCipher, backup, search, revision, and synchronization
services as desktop:

- a gomobile-safe value API with cooperative request cancellation and bounded
  event polling instead of foreign-thread callbacks;
- deterministic AAR/JAR normalization and SHA-256 checksums for the
  SQLCipher-linked Android core;
- English/Russian onboarding and unlock, notebook/tag navigation, virtualized
  notes, local search, Markdown editing and preview, revisions, bounded
  content-URI photo attachments, and airplane-mode saves;
- Android Keystore and strong-biometric wrapping, configurable automatic lock,
  `FLAG_SECURE`, recent-task redaction, and a neutral Flutter privacy surface
  while backgrounded;
- constrained periodic and expedited WorkManager synchronization plus a
  separate network-independent daily backup worker;
- balanced P-256 SPAKE2 confirmation with six-digit codes, bounded
  XChaCha20-Poly1305 bootstrap frames, expiry, replay rejection, mismatch
  abort, and key wiping;
- encrypted private backups mirrored through a user-selected Storage Access
  Framework provider, daily rotation, capacity preflight, bounded import,
  manifest/account-AEAD verification, preview, safety backup, and whole restore;
- all, selected-notebook, and metadata-only attachment policies with a
  configurable LRU limit that never selects pinned files or unsynchronized
  originals;
- encrypted Android-Keystore share/widget handoff, bounded text/link/photo
  capture, post-unlock offline import, and a locked-state-private quick-note
  widget.

## Review decisions

The Go core owns mutable account state and all cryptographic decisions. Flutter
and Kotlin exchange only strings, primitive values, and byte arrays. Android
content providers never receive plaintext account keys, database keys, or note
collections.

Backup publication and import use staging names. Import rejects unsafe names,
depth/count/size overflow, insufficient local capacity, manifest mismatch,
cross-account headers, and AEAD failure before catalog publication. A device
secret envelope is fsynced and atomically renamed; a fully written staging
envelope is recovered after abrupt termination instead of silently replacing
the device secret.

The lifecycle review fixed a stale-unlocked Flutter surface after native
auto-lock and a race that could arm a delayed lock after the activity had
already returned to the foreground.

## Verification

The requested test gate was respected: no tests ran until item 8.13 and the
Android artifact build completed.

| Check | Result |
| --- | --- |
| Gomobile value-boundary validation and mobile Go tests | Pass |
| Flutter analysis | Pass, no issues |
| Normalized SQLCipher AAR plus SHA-256 file | Built |
| Flutter Android debug APK | Built |
| Flutter widget tests | Not executed: the managed sandbox denied the project Flutter SDK lockfile, and the required out-of-sandbox approval was unavailable |
| Android instrumentation | Not executed: `adb` reported no attached device; compiling the instrumentation APK was also blocked by the managed Gradle lockfile permission |

The two unavailable runtime gates are environmental acceptance checks, not
known source failures. They remain required on an Android emulator and physical
arm64 device before a release build is promoted.

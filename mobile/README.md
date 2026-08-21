# Beresta mobile

The Android client is a complete offline-first Flutter application backed by
the same SQLCipher/account/sync Go core as desktop. It supports local account
creation and unlock, notes/notebooks/tags, Markdown editing, local search,
revisions, content-URI attachments, optional server sync, encrypted backups,
retention-aware lazy attachment caching, encrypted share capture, and a
privacy-preserving quick-note widget.

From the repository root, `build.cmd mobile-build-android` produces the
normalized `build/output/beresta-core.aar`, its SHA-256 checksum, and a debug
APK. `build.cmd mobile-test-android` runs SQLCipher, Keystore, privacy, capture,
and WorkManager instrumentation on a connected `arm64-v8a` device.

See [Android user guide](../docs/android-user-guide.md),
[Android build guide](../docs/android-build.md), and
[Android privacy notes](../docs/android-privacy.md).

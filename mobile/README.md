# Beresta mobile

The Android client uses Flutter and consumes a SQLCipher-linked Go core AAR produced by `gomobile bind`.

From the repository root, `build.cmd mobile-build-android` produces the AAR and debug APK. `build.cmd mobile-test-android` runs the encrypted-database instrumentation test on a connected `arm64-v8a` device.

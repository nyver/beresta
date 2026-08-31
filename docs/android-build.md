# Android build and verification

The pinned host toolchain is documented in `README.md`. Configure Android SDK
platform 36, build-tools 36.0.0, NDK 28.2.13676358, Flutter 3.47.0, and the
pinned `gomobile` revision, then run from the repository root:

```powershell
build.cmd mobile-check
build.cmd mobile-bind-android
build.cmd mobile-build-android
```

`mobile-check` validates that every exported `core/mobileapi` method is a
gomobile-safe value boundary. `mobile-bind-android` links SQLCipher for the
supported Android ABIs, recursively normalizes AAR/JAR entry order and
timestamps, and writes `build/output/beresta-core.aar.sha256`.

Run `build.cmd mobile-test-android` with one online `arm64-v8a` device selected
through `adb`. It builds and installs the application and instrumentation APKs,
then requires an explicit successful instrumentation summary. Emulator and
physical-device runs cover the same suite; biometric enrollment-invalidation
acceptance additionally requires physical hardware.

## Release packaging

`build.cmd mobile-package-android` builds a signed release APK and AAB and
writes their SHA-256 digests to `build/output/android-SHA256SUMS`. It requires
an upload keystore, provided through environment variables and never checked
into the repository:

- `BERESTA_ANDROID_KEYSTORE_PATH` — path to the `.jks`/`.keystore` file;
- `BERESTA_ANDROID_KEYSTORE_PASSWORD` — keystore password;
- `BERESTA_ANDROID_KEY_ALIAS` — key alias within the keystore;
- `BERESTA_ANDROID_KEY_PASSWORD` — key password.

Gradle fails the release build closed (see
`mobile/android/app/build.gradle.kts`) rather than falling back to a
debug-signed artifact if any of these are unset.

`build-mobile-release.bat`, at the repository root, wraps
`build.cmd mobile-package-android` for local release builds. It is excluded
from version control (see `.gitignore`) because it sets the four
`BERESTA_ANDROID_*` signing variables directly at the top of the script;
edit those values with your real keystore path, keystore password, key
alias, and key password before running it. The script refuses to run if the
configured keystore path does not exist.

Debug builds use the application ID `app.beresta.notes.debug` (an
`applicationIdSuffix` set in `mobile/android/app/build.gradle.kts`), distinct
from the release app's `app.beresta.notes`. Android refuses to install an APK
over an existing app when the signing certificates differ, so without this
split a debug-signed build and the upload-keystore-signed release build could
not coexist on the same device; the two now install side by side instead.

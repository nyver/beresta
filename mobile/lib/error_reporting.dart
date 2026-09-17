import "package:flutter/foundation.dart";

/// configureErrorReporting installs handlers for uncaught Flutter framework
/// errors and uncaught Dart errors that, outside debug builds, log only the
/// failing value's runtime type before Logcat - never its message or stack
/// trace, which can carry note content, a passphrase, or other sensitive
/// detail embedded in whatever failed (specs/product-experience's "Layered
/// privacy-preserving diagnostics" requirement, mirrored from
/// internal/diagnostics.CrashMetadata's identical "type only, never value"
/// rule). Without this, Flutter's own default handlers print the full
/// exception and stack trace to the console (and so to Logcat on Android)
/// unconditionally, in every build mode. Debug builds keep that normal
/// verbose console output for local development; [debug] defaults to
/// [kDebugMode] and exists only so tests can exercise the release branch
/// without needing an actual release build.
void configureErrorReporting({bool debug = kDebugMode}) {
  if (debug) {
    return;
  }
  FlutterError.onError = (FlutterErrorDetails details) {
    debugPrint("Unhandled Flutter error: ${details.exception.runtimeType}");
  };
  PlatformDispatcher.instance.onError = (Object error, StackTrace stack) {
    debugPrint("Unhandled error: ${error.runtimeType}");
    return true;
  };
}

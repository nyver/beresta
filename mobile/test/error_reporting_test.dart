import "dart:ui";

import "package:beresta/error_reporting.dart";
import "package:flutter/foundation.dart";
import "package:flutter_test/flutter_test.dart";

void main() {
  late FlutterExceptionHandler? originalFlutterOnError;
  late ErrorCallback? originalDispatcherOnError;

  setUp(() {
    originalFlutterOnError = FlutterError.onError;
    originalDispatcherOnError = PlatformDispatcher.instance.onError;
  });

  tearDown(() {
    FlutterError.onError = originalFlutterOnError;
    PlatformDispatcher.instance.onError = originalDispatcherOnError;
  });

  test("debug mode (the default) does not install the release sanitizer", () {
    configureErrorReporting();
    expect(FlutterError.onError, same(originalFlutterOnError));
    expect(
      PlatformDispatcher.instance.onError,
      same(originalDispatcherOnError),
    );
  });

  test(
    "release mode logs only the error's runtime type, never its message or a seeded secret",
    () {
      configureErrorReporting(debug: false);

      final captured = <String>[];
      final previousDebugPrint = debugPrint;
      debugPrint = (String? message, {int? wrapWidth}) {
        if (message != null) captured.add(message);
      };
      addTearDown(() => debugPrint = previousDebugPrint);

      const secret = "seeded-secret-note-body-canary";
      FlutterError.onError!(FlutterErrorDetails(exception: StateError(secret)));
      final handled = PlatformDispatcher.instance.onError!(
        Exception(secret),
        StackTrace.current,
      );

      final combined = captured.join("\n");
      expect(combined, isNot(contains(secret)));
      expect(combined, contains("StateError"));
      expect(combined, contains("Exception"));
      expect(handled, isTrue);
    },
  );
}

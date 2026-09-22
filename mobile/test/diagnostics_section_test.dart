import "package:flutter/material.dart";
import "package:flutter/services.dart";
import "package:flutter_localizations/flutter_localizations.dart";
import "package:flutter_test/flutter_test.dart";

import "package:beresta/app.dart";
import "package:beresta/strings.dart";

import "widget_test.dart" show FakeGateway;

Widget hostDiagnosticsSection(FakeGateway gateway) => MaterialApp(
  locale: const Locale("en"),
  supportedLocales: const [Locale("en"), Locale("ru")],
  localizationsDelegates: const [
    GlobalMaterialLocalizations.delegate,
    GlobalWidgetsLocalizations.delegate,
    GlobalCupertinoLocalizations.delegate,
  ],
  home: Scaffold(
    // A scrollable host, matching how SettingsSheet's ListView always
    // wraps DiagnosticsSection in the real app: the expanded technical
    // details easily exceed a fixed test viewport height.
    body: SingleChildScrollView(
      child: DiagnosticsSection(gateway: gateway, strings: Strings("en")),
    ),
  ),
);

void main() {
  // The clipboard copy test below exercises Clipboard.setData, which
  // TestWidgetsFlutterBinding does not mock by default: without this
  // handler the MethodChannel call never resolves, and handleCopy's
  // await simply never completes.
  TestWidgetsFlutterBinding.ensureInitialized();
  TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
      .setMockMethodCallHandler(SystemChannels.platform, (call) async => null);

  testWidgets("does not fetch diagnostics until the section is expanded", (
    tester,
  ) async {
    final gateway = FakeGateway(unlocked: true);
    await tester.pumpWidget(hostDiagnosticsSection(gateway));
    await tester.pumpAndSettle();

    expect(find.text("0.1.0"), findsNothing);
  });

  testWidgets("loads and shows the summary once expanded", (tester) async {
    final gateway = FakeGateway(unlocked: true);
    gateway.diagnosticSummaryValue = {
      ...gateway.diagnosticSummaryValue,
      "app_version": "9.9.9",
      "platform": "android",
      "pending_count": 3,
    };
    await tester.pumpWidget(hostDiagnosticsSection(gateway));
    await tester.pumpAndSettle();

    await tester.tap(find.text("Diagnostics"));
    await tester.pumpAndSettle();

    expect(find.text("9.9.9"), findsOneWidget);
    expect(find.text("android"), findsOneWidget);
    expect(find.text("3"), findsOneWidget);
  });

  testWidgets("loads technical details only once that section is expanded", (
    tester,
  ) async {
    final gateway = FakeGateway(unlocked: true);
    gateway.technicalDiagnosticsValue = {
      ...gateway.technicalDiagnosticsValue,
      "workspace_id": "ws-canary",
      "device_id": "device-canary",
      "cursor_sequence": 7,
      "cursor_epoch": 1,
    };
    await tester.pumpWidget(hostDiagnosticsSection(gateway));
    await tester.pumpAndSettle();

    await tester.tap(find.text("Diagnostics"));
    await tester.pumpAndSettle();
    expect(find.text("ws-canary"), findsNothing);

    await tester.tap(find.text("Show technical details"));
    await tester.pumpAndSettle();

    expect(find.text("ws-canary"), findsOneWidget);
    expect(find.text("device-canary"), findsOneWidget);
    expect(find.text("7@1"), findsOneWidget);
  });

  testWidgets(
    "copies the sanitized bundle to the clipboard and shows confirmation",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..copyDiagnosticsValue = "Beresta diagnostics\n\nApp version: 9.9.9\n";
      await tester.pumpWidget(hostDiagnosticsSection(gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.text("Diagnostics"));
      await tester.pumpAndSettle();
      await tester.tap(find.text("Copy diagnostics"));
      await tester.pumpAndSettle();

      expect(find.text("Copied"), findsOneWidget);
    },
  );
}

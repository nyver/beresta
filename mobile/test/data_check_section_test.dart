import "package:flutter/material.dart";
import "package:flutter_localizations/flutter_localizations.dart";
import "package:flutter_test/flutter_test.dart";

import "package:beresta/app.dart";
import "package:beresta/strings.dart";

import "widget_test.dart" show FakeGateway;

/// Covers task 7.10's Advanced "Check my data" action: one button that
/// reports either a healthy result or one actionable summary, never the
/// individual internal maintenance jobs it checks, per specs/product-
/// experience's "Routine maintenance and user data check" requirement.
Widget hostDataCheckSection(FakeGateway gateway) => MaterialApp(
  locale: const Locale("en"),
  supportedLocales: const [Locale("en"), Locale("ru")],
  localizationsDelegates: const [
    GlobalMaterialLocalizations.delegate,
    GlobalWidgetsLocalizations.delegate,
    GlobalCupertinoLocalizations.delegate,
  ],
  home: Scaffold(
    body: DataCheckSection(gateway: gateway, strings: Strings("en")),
  ),
);

void main() {
  testWidgets("does not run the check until the button is pressed", (
    tester,
  ) async {
    final gateway = FakeGateway(unlocked: true);
    await tester.pumpWidget(hostDataCheckSection(gateway));
    await tester.pumpAndSettle();

    expect(gateway.runDataCheckCallCount, 0);
  });

  testWidgets("shows a healthy result after running the check", (tester) async {
    final gateway = FakeGateway(unlocked: true);
    await tester.pumpWidget(hostDataCheckSection(gateway));
    await tester.pumpAndSettle();

    await tester.tap(find.text("Check my data"));
    await tester.pumpAndSettle();

    expect(gateway.runDataCheckCallCount, 1);
    expect(find.text("No problems found."), findsOneWidget);
  });

  testWidgets(
    "shows one actionable summary without itemizing internal maintenance jobs",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..dataCheckReportValue = {
          "issue": "backup_needs_attention",
          "healthy": false,
          "checked_at_unix_ms": 0,
        };
      await tester.pumpWidget(hostDataCheckSection(gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.text("Check my data"));
      await tester.pumpAndSettle();

      expect(
        find.text(
          "Your backup needs attention. Create a new backup from Data settings.",
        ),
        findsOneWidget,
      );
    },
  );
}

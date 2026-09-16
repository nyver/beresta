import "dart:async";

import "package:beresta/app.dart";
import "package:beresta/main.dart";
import "package:beresta/markdown_delta.dart";
import "package:beresta/strings.dart";
import "package:flutter/material.dart";
import "package:flutter_localizations/flutter_localizations.dart";
import "package:flutter_quill/flutter_quill.dart";
import "package:flutter_test/flutter_test.dart";

import "widget_test.dart" show FakeGateway;

/// A minimal host for [EditorScreen] alone, without the rest of
/// [BerestaApp]'s navigation shell - used by the process-recreation test
/// below to rebuild a genuinely fresh [EditorScreen] widget/state (a
/// distinct widget subtree Flutter must dispose and recreate from
/// scratch, mirroring what a killed-and-restarted Android process loses:
/// every in-memory commit-controller field, but nothing the gateway
/// itself durably holds).
Widget hostEditor(FakeGateway gateway, String noteId) => MaterialApp(
  locale: const Locale("en"),
  supportedLocales: const [Locale("en"), Locale("ru")],
  localizationsDelegates: const [
    FlutterQuillLocalizations.delegate,
    GlobalMaterialLocalizations.delegate,
    GlobalWidgetsLocalizations.delegate,
    GlobalCupertinoLocalizations.delegate,
  ],
  home: EditorScreen(gateway: gateway, strings: Strings("en"), noteId: noteId),
);

void main() {
  testWidgets(
    "cancels a superseded in-flight commit and only persists the newer content",
    (tester) async {
      final gateway = FakeGateway(unlocked: true);
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();
      await tester.tap(find.byIcon(Icons.add));
      await tester.pumpAndSettle();

      final controller =
          tester.widget<QuillEditor>(find.byType(QuillEditor)).controller;

      gateway.holdSaveNote = Completer<void>();
      controller.replaceText(
        0,
        0,
        "First",
        const TextSelection.collapsed(offset: 5),
      );
      // Fires the debounced commit, which then blocks on holdSaveNote
      // instead of resolving - simulating a slow in-flight request.
      await tester.pump(const Duration(milliseconds: 900));

      expect(gateway.saveRequestIds, hasLength(1));
      final firstRequestId = gateway.saveRequestIds.single;

      controller.replaceText(
        5,
        0,
        " second",
        const TextSelection.collapsed(offset: 12),
      );
      await tester.pump(const Duration(milliseconds: 900));

      // The newer edit's commit canceled the still-in-flight older one
      // instead of letting both race against the same base state.
      expect(gateway.canceledRequestIds, contains(firstRequestId));
      expect(gateway.saveRequestIds, hasLength(2));

      gateway.holdSaveNote!.complete();
      await tester.pumpAndSettle();

      expect(gateway.savedBody, "First second");
      expect(find.text("Saved"), findsOneWidget);
    },
  );

  testWidgets("shows could_not_save then saved once a retry succeeds", (
    tester,
  ) async {
    final gateway = FakeGateway(unlocked: true);
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();
    await tester.tap(find.byIcon(Icons.add));
    await tester.pumpAndSettle();

    final controller =
        tester.widget<QuillEditor>(find.byType(QuillEditor)).controller;

    gateway.nextSaveNoteFailure = Exception("disk full");
    controller.replaceText(
      0,
      0,
      "Hello",
      const TextSelection.collapsed(offset: 5),
    );
    await tester.pump(const Duration(milliseconds: 900));
    await tester.pumpAndSettle();

    expect(find.text("Could not save"), findsOneWidget);
    expect(gateway.savedBody, isEmpty);

    // The failed content stays dirty (see _EditorScreenState.commit), so
    // a further edit's debounce resends the full current text, not just
    // the new character - mirroring desktop's retained-buffer retry.
    controller.replaceText(5, 0, "!", const TextSelection.collapsed(offset: 6));
    await tester.pump(const Duration(milliseconds: 900));
    await tester.pumpAndSettle();

    expect(find.text("Saved"), findsOneWidget);
    expect(gateway.savedBody, "Hello!");
  });

  testWidgets(
    "a fresh editor session after process recreation starts clean and reads durably saved content",
    (tester) async {
      final gateway = FakeGateway(unlocked: true);
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();
      await tester.tap(find.byIcon(Icons.add));
      await tester.pumpAndSettle();

      final controller =
          tester.widget<QuillEditor>(find.byType(QuillEditor)).controller;
      controller.replaceText(
        0,
        0,
        "Before kill",
        const TextSelection.collapsed(offset: 11),
      );
      await tester.pump(const Duration(milliseconds: 900));
      await tester.pumpAndSettle();

      expect(gateway.savedBody, "Before kill");
      expect(find.text("Saved"), findsOneWidget);

      // Simulate Android killing and recreating the process: tearing down
      // this widget tree entirely and rebuilding a brand-new EditorScreen
      // for the same note mirrors what a real process kill destroys (the
      // Dart isolate, and with it every CommitTracker generation and
      // in-flight request id this session ever held) while reading from
      // the same gateway, representing the on-disk storage a kill never
      // touches.
      await tester.pumpWidget(const SizedBox.shrink());
      final noteId = gateway.note["id"] as String;
      await tester.pumpWidget(hostEditor(gateway, noteId));
      await tester.pumpAndSettle();

      expect(find.text("Saved"), findsOneWidget);
      final reopened =
          tester.widget<QuillEditor>(find.byType(QuillEditor)).controller;
      expect(
        deltaToMarkdown(reopened.document.toDelta()).trim(),
        "Before kill",
      );

      // A fresh edit in the new session commits on its own fresh
      // generation, unaffected by anything the killed session's now-
      // discarded in-memory state held.
      reopened.replaceText(
        11,
        0,
        " after restart",
        const TextSelection.collapsed(offset: 25),
      );
      await tester.pump(const Duration(milliseconds: 900));
      await tester.pumpAndSettle();

      expect(gateway.savedBody, "Before kill after restart");
      expect(find.text("Saved"), findsOneWidget);
    },
  );
}

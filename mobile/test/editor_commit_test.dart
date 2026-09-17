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
  home: EditorScreen(
    gateway: gateway,
    strings: Strings("en"),
    noteId: noteId,
    onNoteListChanged: () {},
  ),
);

/// Opens [ServerSheet] as a modal bottom sheet, mirroring how
/// [showServer] in app.dart presents it in production - unlike hosting it
/// directly as a route's `home`, this gives it a route to pop back to, so
/// [ServerSheet]'s own `Navigator.pop(context)` after a successful
/// workspace switch behaves exactly as it does in the real app instead of
/// popping the test host's only route.
Widget hostServerSheet(FakeGateway gateway) => MaterialApp(
  locale: const Locale("en"),
  supportedLocales: const [Locale("en"), Locale("ru")],
  localizationsDelegates: const [
    GlobalMaterialLocalizations.delegate,
    GlobalWidgetsLocalizations.delegate,
    GlobalCupertinoLocalizations.delegate,
  ],
  home: Builder(
    builder:
        (context) => Scaffold(
          body: Center(
            child: ElevatedButton(
              onPressed:
                  () => showModalBottomSheet<void>(
                    context: context,
                    isScrollControlled: true,
                    showDragHandle: true,
                    builder:
                        (_) => ServerSheet(
                          gateway: gateway,
                          strings: Strings("en"),
                        ),
                  ),
              child: const Text("open"),
            ),
          ),
        ),
  ),
);

void main() {
  group("ActiveEditorFlush", () {
    test("flushIfAny is a no-op with nothing registered", () async {
      await ActiveEditorFlush.flushIfAny();
    });

    test("register then flushIfAny invokes the registered flush", () async {
      var called = 0;
      Future<void> flush() async => called++;
      ActiveEditorFlush.register(flush);
      addTearDown(() => ActiveEditorFlush.unregister(flush));

      await ActiveEditorFlush.flushIfAny();

      expect(called, 1);
    });

    test("unregister stops flushIfAny from invoking it", () async {
      var called = 0;
      Future<void> flush() async => called++;
      ActiveEditorFlush.register(flush);
      ActiveEditorFlush.unregister(flush);

      await ActiveEditorFlush.flushIfAny();

      expect(called, 0);
    });

    test(
      "a stale unregister for a superseded registration does not clear the current one",
      () async {
        // Mirrors a disposed EditorScreen's dispose() racing a newly
        // pushed one's initState(): the stale unregister must not evict
        // the screen that is actually open now.
        var firstCalled = 0;
        var secondCalled = 0;
        Future<void> first() async => firstCalled++;
        Future<void> second() async => secondCalled++;
        ActiveEditorFlush.register(first);
        ActiveEditorFlush.register(second);
        addTearDown(() => ActiveEditorFlush.unregister(second));

        ActiveEditorFlush.unregister(first);
        await ActiveEditorFlush.flushIfAny();

        expect(firstCalled, 0);
        expect(secondCalled, 1);
      },
    );
  });

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
    "flushes the open editor's pending edit when the app backgrounds",
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
        "Backgrounded edit",
        const TextSelection.collapsed(offset: 18),
      );
      // Before the 800ms debounce would have committed on its own - the
      // real race this guards against (Home button pressed mid-typing).
      await tester.pump();
      expect(gateway.savedBody, isEmpty);

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await tester.pumpAndSettle();

      expect(gateway.savedBody, "Backgrounded edit");
    },
  );

  testWidgets(
    "flushes a pending body edit before restoring a revision, so it cannot reappear after the restore",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..revisionList = [
          {"id": "rev-1", "checkpoint": true, "created_unix_ms": 1710000000000},
        ];
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();
      await tester.tap(find.byIcon(Icons.add));
      await tester.pumpAndSettle();

      final controller =
          tester.widget<QuillEditor>(find.byType(QuillEditor)).controller;
      controller.replaceText(
        0,
        0,
        "Pending edit",
        const TextSelection.collapsed(offset: 12),
      );
      // Open Revisions immediately, before the 800ms debounce would have
      // committed on its own - the real race this guards against.
      await tester.pump();
      expect(gateway.savedBody, isEmpty);

      await tester.tap(find.text("Revisions"));
      await tester.pumpAndSettle();
      final revisionDate =
          DateTime.fromMillisecondsSinceEpoch(
            1710000000000,
          ).toLocal().toString();
      await tester.tap(find.text(revisionDate));
      await tester.pumpAndSettle();
      await tester.tap(find.text("Restore"));
      await tester.pumpAndSettle();

      // The pending edit committed before restoreRevision ran, instead of
      // being silently discarded or reappearing on top of the restored
      // content once the editor reloads it.
      expect(gateway.savedBody, "Pending edit");
    },
  );

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

  testWidgets(
    "flushes the active editor's flush barrier before switching the active workspace",
    (tester) async {
      final events = <String>[];
      final gateway =
          FakeGateway(unlocked: true)
            ..syncStatusValue = "current"
            ..workspacesToReturn = [
              {
                "workspace_id": "ws-2",
                "role": "member",
                "member_count": 2,
                "active": false,
              },
            ];
      gateway.onSetActiveWorkspace = (id) => events.add("switch:$id");

      Future<void> flush() async => events.add("flush");
      ActiveEditorFlush.register(flush);
      addTearDown(() => ActiveEditorFlush.unregister(flush));

      await tester.pumpWidget(hostServerSheet(gateway));
      await tester.pumpAndSettle();
      await tester.tap(find.text("open"));
      await tester.pumpAndSettle();
      // A second settle: ServerSheet's several independent initState
      // futures (identity, connection info, sync summary/quarantine,
      // workspaces) can still have one resolve and schedule its setState
      // just as the first pumpAndSettle finishes; without this the
      // workspaces list can intermittently still be empty here.
      await tester.pumpAndSettle();

      await tester.ensureVisible(find.text("Switch"));
      await tester.pumpAndSettle();
      await tester.tap(find.text("Switch"));
      await tester.pumpAndSettle();

      expect(
        events,
        ["flush", "switch:ws-2"],
        reason:
            "the registered editor flush must run before the workspace switch reaches the gateway",
      );
    },
  );

  testWidgets(
    "shows an unsafe incoming operation's sanitized details and retries it",
    (tester) async {
      final gateway =
          FakeGateway(unlocked: true)
            ..syncStatusValue = "action_required"
            ..quarantineEntries = [
              {
                "operation_id": "018f0000-0000-7000-8000-000000000099",
                "sequence": 5,
                "reason": "verification_failed",
                "received_unix_ms": 1710000000000,
              },
            ];

      await tester.pumpWidget(hostServerSheet(gateway));
      await tester.pumpAndSettle();
      await tester.tap(find.text("open"));
      await tester.pumpAndSettle();

      expect(
        find.text("018f0000-0000-7000-8000-000000000099"),
        findsOneWidget,
      );
      expect(find.text("verification_failed"), findsOneWidget);

      await tester.tap(find.text("Retry"));
      await tester.pumpAndSettle();

      expect(
        find.text("018f0000-0000-7000-8000-000000000099"),
        findsNothing,
      );
      expect(find.text("No unsafe incoming operations."), findsOneWidget);
    },
  );
}

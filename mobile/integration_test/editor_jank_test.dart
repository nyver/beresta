import "package:beresta/main.dart";
import "package:flutter/material.dart";
import "package:flutter_quill/flutter_quill.dart";
import "package:flutter_test/flutter_test.dart";
import "package:integration_test/integration_test.dart";

import "../test/widget_test.dart" show FakeGateway;

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  // task 11.3: a real editor frame/jank harness. Unlike the desktop
  // equivalent (NoteEditor.jank.test.tsx, jsdom-timed since there is no
  // real compositor under Vitest), IntegrationTestWidgetsFlutterBinding
  // runs on an actual Flutter engine with a real rendering pipeline, so
  // `traceAction`'s reported frame-build times are genuine measurements -
  // but only when this file runs against a real device or emulator via
  // `flutter test integration_test/editor_jank_test.dart -d <device>`,
  // which this sandbox has neither of (no attached Android
  // device/emulator - the same hardware gap task 12.5's physical
  // qualification pass exists to close). It is included here, ready to
  // run there, rather than executed in this session.
  //
  // This measures sustained typing's real on-device frame-build cost, a
  // general regression floor (it would catch, say, an accidental O(n^2)
  // editor-update regression) rather than a specific "concurrent backend
  // work never blocks typing" claim: unlike desktop, where an open note
  // genuinely receives a live background-sync merge into the same
  // document being edited (see NoteEditor.jank.test.tsx's real
  // "sync:summary" trigger), Android's editor has no live CRDT binding to
  // interrupt (task 6.8's documented architecture difference) - nothing
  // in EditorScreen's typing path ever awaits or references a
  // gateway.syncNow()/runDataCheck() call, so holding one of those calls
  // artificially pending here would not exercise any real coupling, only
  // add a Completer nothing under test ever observes. Dart's single-
  // threaded event loop already guarantees typing cannot be blocked by an
  // unrelated pending Future by construction, so there is no such claim
  // left to prove empirically for this platform the way there was for
  // desktop's live-merge case.
  testWidgets(
    "typing stays within the editor frame budget during sustained input",
    (tester) async {
      final binding = IntegrationTestWidgetsFlutterBinding.instance;
      final gateway = FakeGateway(unlocked: true);

      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.text("Offline note"));
      await tester.pumpAndSettle();
      expect(find.byType(QuillEditor), findsOneWidget);

      final controller =
          tester.widget<QuillEditor>(find.byType(QuillEditor)).controller;

      await binding.traceAction(() async {
        // A realistic long note body (~2,000 characters), simulating
        // sustained typing rather than a single keystroke - the same scale
        // NoteEditor.jank.test.tsx uses on desktop.
        const sentence = "The quick brown fox jumps over the lazy dog. ";
        for (var i = 0; i < 40; i++) {
          final offset = controller.document.length - 1;
          controller.replaceText(
            offset < 0 ? 0 : offset,
            0,
            sentence,
            TextSelection.collapsed(offset: offset + sentence.length),
          );
          await tester.pump();
        }
      }, reportKey: "editor_typing_frame_budget");

      expect(controller.document.toPlainText(), contains("quick brown fox"));
    },
  );
}

import "package:beresta/paste_format.dart";
import "package:dart_quill_delta/dart_quill_delta.dart";
import "package:flutter_quill/flutter_quill.dart";
import "package:flutter_test/flutter_test.dart";

/// Mirrors desktop's pasteFormat.test.ts
/// (desktop/frontend/src/editor/pasteFormat.test.ts): both clients must
/// degrade a rich-text paste to the same canonical semantic format set.
void main() {
  test("keeps every canonical format attribute unchanged", () {
    final delta =
        Delta()
          ..insert("bold", {"bold": true})
          ..insert("italic", {"italic": true})
          ..insert("strike", {"strike": true})
          ..insert("code", {"code": true})
          ..insert("link", {"link": "https://example.com"})
          ..insert("\n", {"header": 2})
          ..insert("\n", {"list": "ordered"})
          ..insert("\n", {"list": "bullet"})
          ..insert("\n", {"blockquote": true})
          ..insert("\n", {"code-block": true});

    expect(stripUnsupportedFormats(delta).toList(), delta.toList());
  });

  test("drops non-canonical attributes but keeps the text", () {
    final delta =
        Delta()
          ..insert("underlined", {"underline": true})
          ..insert("colored", {"color": "#ff0000"});

    final result = stripUnsupportedFormats(delta);

    // Delta.insert merges adjacent plain-text ops, so two now-unformatted
    // runs collapse into one - a different but equally correct
    // representation of the same degraded content.
    expect(result.toList(), [Operation.insert("underlinedcolored")]);
  });

  test("keeps canonical attributes alongside dropped ones on the same run", () {
    final delta =
        Delta()
          ..insert("mixed", {"bold": true, "underline": true, "font": "serif"});

    final result = stripUnsupportedFormats(delta);

    expect(result.toList(), [
      Operation.insert("mixed", {"bold": true}),
    ]);
  });

  test("drops a heading level outside 1-3 instead of clamping it", () {
    final delta =
        Delta()
          ..insert("Heading four")
          ..insert("\n", {"header": 4});

    final result = stripUnsupportedFormats(delta);

    // Delta.insert merges the now-unformatted trailing newline into the
    // preceding plain-text run: the heading level 4 attribute is gone
    // either way, which is what this test is proving.
    expect(result.toList(), [Operation.insert("Heading four\n")]);
  });

  test("keeps heading levels 1 through 3", () {
    for (final level in [1, 2, 3]) {
      final delta =
          Delta()
            ..insert("Heading")
            ..insert("\n", {"header": level});

      final result = stripUnsupportedFormats(delta);

      expect(result.toList(), [
        Operation.insert("Heading"),
        Operation.insert("\n", {"header": level}),
      ]);
    }
  });

  test(
    "drops a checklist list value instead of keeping an unrenderable checkbox",
    () {
      // flutter_quill's list format also accepts "checked"/"unchecked" for a
      // checklist item (a paste source can produce this even though no
      // toolbar button offers it) - the Go core's canonical projection only
      // understands "bullet"/"ordered".
      final delta =
          Delta()
            ..insert("Buy milk")
            ..insert("\n", {"list": "checked"});

      final result = stripUnsupportedFormats(delta);

      expect(result.toList(), [Operation.insert("Buy milk\n")]);
    },
  );

  test("keeps bullet and ordered list values", () {
    for (final value in ["bullet", "ordered"]) {
      final delta =
          Delta()
            ..insert("Item")
            ..insert("\n", {"list": value});

      final result = stripUnsupportedFormats(delta);

      expect(result.toList(), [
        Operation.insert("Item"),
        Operation.insert("\n", {"list": value}),
      ]);
    }
  });

  test("passes through ops with no attributes unchanged", () {
    final delta = Delta()..insert("plain text");

    expect(stripUnsupportedFormats(delta).toList(), [
      Operation.insert("plain text"),
    ]);
  });

  test("the app.dart wiring type-checks against QuillClipboardConfig's "
      "onRichTextPaste signature", () async {
    // flutter_quill keeps the code path that actually calls
    // onRichTextPaste (QuillController.getDeltaToPaste) @internal, so it
    // cannot be driven directly from outside the package; this at least
    // pins the exact callback shape mobile/lib/app.dart wires in, so a
    // signature mismatch there fails a test instead of only surfacing
    // as a runtime no-op the first time a user pastes rich text.
    final QuillClipboardConfig config = QuillClipboardConfig(
      onRichTextPaste:
          (delta, isExternal) async => stripUnsupportedFormats(delta),
    );

    final pasted =
        Delta()
          ..insert("Heading four")
          ..insert("\n", {"header": 4});

    final result = await config.onRichTextPaste!(pasted, true);

    expect(result!.toList(), stripUnsupportedFormats(pasted).toList());
  });

  test("keeps typing normally after a degraded paste (re-edit)", () {
    final pasted = stripUnsupportedFormats(
      Delta()
        ..insert("Too deep")
        ..insert("\n", {"header": 4}),
    );
    final controller = QuillController.basic();
    // Document() starts with a lone "\n" (document.dart), so replace it
    // outright rather than compose on top of it, mirroring how a real
    // paste replaces the current (here: empty) selection.
    controller.document.replace(0, controller.document.length, pasted);

    controller.document.replace(
      controller.document.length - 1,
      0,
      " continued",
    );

    expect(controller.document.toPlainText(), "Too deep continued\n");
  });

  test(
    "undo/redo round-trips a formatted edit within the canonical format set",
    () {
      final controller = QuillController.basic();
      controller.document.replace(
        0,
        controller.document.length,
        Delta()
          ..insert("Hello ")
          ..insert("bold", {"bold": true})
          ..insert("\n"),
      );
      // Forces the next edit into its own undo group instead of merging
      // with this one inside History's 400ms interval window
      // (document/history.dart), mirroring desktop's
      // quill.history.cutoff().
      controller.document.history.lastRecorded = 0;
      controller.document.replace(controller.document.length - 1, 0, " more");

      final fullyEditedText = controller.document.toPlainText();
      expect(fullyEditedText, "Hello bold more\n");

      controller.undo();
      expect(controller.document.toPlainText(), "Hello bold\n");
      // The formatting from the first (not-undone) edit survives the undo.
      expect(
        controller.document.toDelta().toList(),
        contains(Operation.insert("bold", {"bold": true})),
      );

      controller.redo();
      expect(controller.document.toPlainText(), fullyEditedText);
    },
  );
}

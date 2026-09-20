import "dart:async";
import "dart:typed_data";

import "package:beresta/main.dart";
import "package:flutter/material.dart";
import "package:flutter/services.dart" show PlatformException;
import "package:flutter_quill/flutter_quill.dart";
import "package:flutter_test/flutter_test.dart";
import "package:qr_flutter/qr_flutter.dart";

void main() {
  testWidgets("onboarding is local-first and switches language", (
    tester,
  ) async {
    final gateway = FakeGateway(unlocked: false);
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();

    expect(find.text("Create local account"), findsOneWidget);
    expect(find.textContaining("only on this device"), findsOneWidget);
    await tester.tap(find.text("RU"));
    await tester.pumpAndSettle();
    expect(find.text("Создать локальный аккаунт"), findsOneWidget);
  });

  testWidgets("offline note editing persists through the core boundary", (
    tester,
  ) async {
    final gateway = FakeGateway(unlocked: true);
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.add));
    await tester.pumpAndSettle();
    // QuillController.replaceText is the same high-level entry point the
    // real on-screen keyboard drives internally; going through it directly
    // (rather than simulating IME TextInputClient calls) exercises the
    // editor/controller wiring under test without depending on flutter_
    // quill's own IME-diffing internals, which are unrelated to what this
    // test verifies.
    const enteredText = "Offline paragraph";
    final controller =
        tester.widget<QuillEditor>(find.byType(QuillEditor)).controller;
    controller.replaceText(
      0,
      0,
      enteredText,
      const TextSelection.collapsed(offset: enteredText.length),
    );
    // The body controller's listener debounces a commit 800ms after the
    // last edit (see _EditorScreenState.markDirty/commit); advancing past
    // that fires it without a manual save action, matching the "durable
    // automatic save" product requirement (no save button exists).
    await tester.pump(const Duration(milliseconds: 900));

    expect(gateway.savedBody, enteredText);
  });

  testWidgets(
    "each commit sends the base_revision the previous one returned, not a stale one (task 6.8)",
    (tester) async {
      // Regression coverage for the Dart side of task 6.8's fix: SaveNote's
      // three-way merge only works if the caller actually threads
      // base_revision from GetNote/the previous save into the next one -
      // core/mobileapi's own tests cover the merge itself, this covers the
      // editor wiring around it.
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
        "first",
        const TextSelection.collapsed(offset: 5),
      );
      await tester.pump(const Duration(milliseconds: 900));
      expect(gateway.saveBaseRevisions, ["base-0"]);
      final revisionAfterFirstCommit = gateway.noteBaseRevision;
      expect(revisionAfterFirstCommit, isNot("base-0"));

      controller.replaceText(
        5,
        0,
        " second",
        const TextSelection.collapsed(offset: 12),
      );
      await tester.pump(const Duration(milliseconds: 900));

      expect(gateway.saveBaseRevisions, ["base-0", revisionAfterFirstCommit]);
    },
  );

  testWidgets(
    "creating a local account shows the optional sync prompt, and Skip enters the shell without opening it (task 7.2)",
    (tester) async {
      final gateway = FakeGateway(unlocked: false);
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.enterText(
        find.widgetWithText(TextField, "Passphrase"),
        "correct horse battery staple",
      );
      await tester.tap(find.text("Create local account"));
      await tester.pumpAndSettle();

      expect(find.text("Connect to a sync server?"), findsOneWidget);
      expect(find.text("Create local account"), findsNothing);

      await tester.tap(find.text("Skip for now"));
      await tester.pumpAndSettle();

      expect(find.text("Offline note"), findsOneWidget);
      expect(find.widgetWithText(TextField, "HTTPS server URL"), findsNothing);
    },
  );

  testWidgets(
    "connecting from the sync prompt enters the shell with the server sheet already open (task 7.2)",
    (tester) async {
      final gateway = FakeGateway(unlocked: false);
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.enterText(
        find.widgetWithText(TextField, "Passphrase"),
        "correct horse battery staple",
      );
      await tester.tap(find.text("Create local account"));
      await tester.pumpAndSettle();

      await tester.tap(find.text("Connect now"));
      await tester.pumpAndSettle();

      expect(
        find.widgetWithText(TextField, "Connection code"),
        findsOneWidget,
      );
    },
  );

  testWidgets(
    "revision history lists newest first with a checkpoint badge and shows a diff",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..revisionList = [
          {"id": "rev-1", "checkpoint": true, "created_unix_ms": 1710000000000},
          {
            "id": "rev-2",
            "checkpoint": false,
            "created_unix_ms": 1710000100000,
          },
        ];
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.add));
      await tester.pumpAndSettle();
      await tester.tap(find.text("Previous versions"));
      await tester.pumpAndSettle();

      final oldestDate =
          DateTime.fromMillisecondsSinceEpoch(
            1710000000000,
          ).toLocal().toString();
      final newestDate =
          DateTime.fromMillisecondsSinceEpoch(
            1710000100000,
          ).toLocal().toString();
      expect(find.byType(ListTile), findsNWidgets(2));
      expect(find.text("Checkpoint"), findsOneWidget);
      // Newest first: the later revision's row sits above the older one's.
      expect(
        tester.getTopLeft(find.text(newestDate)).dy,
        lessThan(tester.getTopLeft(find.text(oldestDate)).dy),
      );

      await tester.tap(find.text(newestDate));
      await tester.pumpAndSettle();

      // Diffed against the revision immediately before it in oldest-first
      // order (rev-1), not against empty content.
      expect(find.textContaining("from rev-1"), findsOneWidget);
      expect(find.textContaining("to rev-2"), findsOneWidget);
      expect(find.text("Preview"), findsOneWidget);
      // task 6.6: restoring must explain it creates a new current version
      // rather than destroying history.
      expect(
        find.text(
          "Restoring makes this the current version. The version it replaces stays in history.",
        ),
        findsOneWidget,
      );

      await tester.tap(find.text("Restore"));
      await tester.pumpAndSettle();

      expect(gateway.restoredRevisionId, "rev-2");
    },
  );

  testWidgets("unlocks an existing account with device authentication", (
    tester,
  ) async {
    final gateway =
        FakeGateway(unlocked: false)
          ..accountExists = true
          ..deviceUnlockAvailable = true;
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();

    expect(gateway.deviceUnlockCalls, 1);
    expect(find.text("Offline note"), findsOneWidget);
  });

  testWidgets(
    "cancelling the biometric prompt from the manual retry button shows no credential error (task 7.5)",
    (tester) async {
      final gateway =
          FakeGateway(unlocked: false)
            ..accountExists = true
            ..deviceUnlockAvailable = true
            ..deviceUnlockError = PlatformException(
              code: "device_authentication_canceled",
            );
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      // The automatic attempt on cold start (loadSession) also cancels,
      // and falls through to the ordinary unlock screen without an error.
      expect(gateway.deviceUnlockCalls, 1);
      expect(
        find.textContaining("Could not complete the action"),
        findsNothing,
      );
      final deviceUnlockButton = find.widgetWithText(
        OutlinedButton,
        "Unlock with biometrics or PIN",
      );
      expect(deviceUnlockButton, findsOneWidget);

      await tester.tap(deviceUnlockButton);
      await tester.pumpAndSettle();

      expect(gateway.deviceUnlockCalls, 2);
      expect(
        find.textContaining("Could not complete the action"),
        findsNothing,
      );
      // The account is still valid and password unlock remains available.
      expect(
        find.widgetWithText(TextField, "Passphrase"),
        findsOneWidget,
      );
      expect(find.widgetWithText(FilledButton, "Unlock"), findsOneWidget);
    },
  );

  testWidgets(
    "an existing empty-titled note displays as Untitled in the list",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..listedNotes = [
          {...FakeGateway.noteFixture, "title": ""},
        ];
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      expect(find.text("Untitled"), findsOneWidget);
    },
  );

  testWidgets(
    "a newly created note's title is never stored or synced as the literal placeholder",
    (tester) async {
      // Placeholder titles SHALL not become stored titles unless edited
      // (specs/notes-management): unlike desktop's own "" convention this
      // guards the same claim on mobile, where createNote used to persist
      // the literal localized "Untitled" immediately - a real note (and
      // sync traffic) even for a draft nobody has touched yet.
      final gateway = FakeGateway(unlocked: true);
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.add));
      await tester.pumpAndSettle();

      expect(gateway.createdNoteTitle, isEmpty);
    },
  );

  testWidgets(
    "a newly created note's title field starts empty and focused, with no modal",
    (tester) async {
      // FakeGateway.createNote (unlike the real gateway) always returns its
      // one static note fixture rather than a fresh note honoring the
      // title it was called with, so this mirrors what a real createNote(
      // "") call would hand back: an empty stored title.
      final gateway = FakeGateway(unlocked: true)..note["title"] = "";
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.add));
      await tester.pumpAndSettle();

      final titleField = find.descendant(
        of: find.byType(AppBar),
        matching: find.byType(TextField),
      );
      final widget = tester.widget<TextField>(titleField);
      expect(widget.controller!.text, isEmpty);
      expect(widget.focusNode!.hasFocus, isTrue);
    },
  );

  testWidgets(
    "removes an untouched empty draft when the user backs out without editing it",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)..note["title"] = "";
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.add));
      await tester.pumpAndSettle();

      await tester.pageBack();
      await tester.pumpAndSettle();

      // A real gateway.deleteNote(widget.noteId, true) call, matching the
      // manual-delete path's own assertion style (see "a note can be
      // deleted from the editor" above).
      expect(gateway.note["deleted"], true);
    },
  );

  testWidgets(
    "keeps a newly created note the user has started typing in when backing out",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)..note["title"] = "";
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.add));
      await tester.pumpAndSettle();

      final controller =
          tester.widget<QuillEditor>(find.byType(QuillEditor)).controller;
      controller.replaceText(
        0,
        0,
        "An actual idea",
        const TextSelection.collapsed(offset: 15),
      );
      await tester.pump();

      await tester.pageBack();
      // The pending edit still needs to flush (PopScope blocks the pop
      // while dirty) before the route actually closes.
      await tester.pumpAndSettle();

      expect(gateway.note["deleted"], isNot(true));
      expect(gateway.savedBody, "An actual idea");
    },
  );

  testWidgets(
    "does not delete a pre-existing empty note when merely opening and leaving it",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)..note["title"] = "";
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      // Opened by tapping an existing list entry, not the "+" create
      // action - autoFocusTitle (and so untouched-draft cleanup) must
      // never apply here, however empty this note already was.
      await tester.tap(find.text("Untitled"));
      await tester.pumpAndSettle();

      await tester.pageBack();
      await tester.pumpAndSettle();

      expect(gateway.note["deleted"], isNot(true));
    },
  );

  testWidgets(
    "typing a plain query matches titles by partial, case-insensitive substring without hitting the backend",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..listedNotes = [
          {...FakeGateway.noteFixture, "id": "note-1", "title": "Running list"},
          {...FakeGateway.noteFixture, "id": "note-2", "title": "Grocery list"},
        ];
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      expect(find.text("Running list"), findsOneWidget);
      expect(find.text("Grocery list"), findsOneWidget);

      // Uppercase and a mid-word (not just prefix) fragment of "Running".
      await tester.enterText(find.byType(SearchBar), "UNN");
      await tester.pump();

      expect(find.text("Running list"), findsOneWidget);
      expect(find.text("Grocery list"), findsNothing);
      expect(gateway.searchCalls, 0);

      // A filter-language token routes to the backend instead.
      await tester.enterText(find.byType(SearchBar), "tag:urgent");
      await tester.pump(const Duration(milliseconds: 300));

      expect(gateway.searchCalls, 1);
    },
  );

  testWidgets("notebook menu creates a root note", (tester) async {
    final gateway = FakeGateway(unlocked: true);
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip("More actions"));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(PopupMenuItem<String>, "New note"));
    await tester.pumpAndSettle();

    expect(gateway.createdNoteNotebookId, "");
    expect(find.byType(QuillEditor), findsOneWidget);
  });

  testWidgets("a notebook can be renamed from a popup dialog", (tester) async {
    final gateway = FakeGateway(unlocked: true)
      ..notebooksList = [
        {"id": "notebook-1", "parent_id": "", "name": "Old name"},
      ];
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();
    await tester.tap(
      find.descendant(
        of: find.widgetWithText(ListTile, "Old name"),
        matching: find.byIcon(Icons.more_vert),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(PopupMenuItem<String>, "Rename"));
    await tester.pumpAndSettle();
    expect(find.text("Rename notebook"), findsOneWidget);

    await tester.enterText(
      find.descendant(
        of: find.byType(AlertDialog),
        matching: find.byType(TextField),
      ),
      "New name",
    );
    await tester.tap(find.widgetWithText(FilledButton, "Rename"));
    await tester.pumpAndSettle();

    expect(gateway.renamedNotebook, ("notebook-1", "New name"));
  });

  testWidgets("attachment can be deleted from its visible action", (
    tester,
  ) async {
    final gateway = FakeGateway(unlocked: true)
      ..attachmentList = [
        {"blob_id": "attachment-1", "media_type": "application/pdf"},
      ];
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();

    await tester.tap(find.text("Offline note"));
    await tester.pumpAndSettle();
    await tester.tap(find.byIcon(Icons.delete_outline));
    await tester.pumpAndSettle();
    expect(find.text("Delete this attachment?"), findsOneWidget);

    await tester.tap(find.widgetWithText(FilledButton, "Delete"));
    await tester.pumpAndSettle();
    expect(gateway.removedAttachment, (
      "018f0000-0000-7000-8000-000000000001",
      "attachment-1",
    ));
  });

  testWidgets(
    "adding a photo shows an in-progress label and refreshes the list on success (task 6.5)",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..capturePhotoCompleter = Completer<void>();
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.text("Offline note"));
      await tester.pumpAndSettle();
      expect(find.text("Add photo"), findsOneWidget);

      await tester.tap(find.text("Add photo"));
      await tester.pump();

      expect(find.text("Adding photo…"), findsOneWidget);
      expect(find.text("Add photo"), findsNothing);

      gateway.capturePhotoCompleter!.complete();
      await tester.pumpAndSettle();

      expect(find.text("Add photo"), findsOneWidget);
      expect(gateway.capturePhotoCalls, 1);
    },
  );

  testWidgets(
    "backing out of the photo picker shows no error (task 6.5's consistent cancellation)",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..capturePhotoFailure = PlatformException(code: "canceled");
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.text("Offline note"));
      await tester.pumpAndSettle();
      await tester.tap(find.text("Add photo"));
      await tester.pumpAndSettle();

      expect(find.byType(SnackBar), findsNothing);
      expect(find.text("Add photo"), findsOneWidget);
    },
  );

  testWidgets(
    "a failed photo add offers Retry, which retries the same action (task 6.5)",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..capturePhotoFailure = PlatformException(
          code: "capture_failed",
          message: "boom",
        );
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.text("Offline note"));
      await tester.pumpAndSettle();
      await tester.tap(find.text("Add photo"));
      await tester.pumpAndSettle();

      expect(find.byType(SnackBar), findsOneWidget);
      expect(gateway.capturePhotoCalls, 1);

      await tester.tap(find.text("Retry"));
      await tester.pumpAndSettle();

      expect(gateway.capturePhotoCalls, 2);
    },
  );

  testWidgets(
    "a note is deleted immediately from the editor, without a confirmation prompt, and offers undo",
    (tester) async {
      // Recoverable ordinary note deletion (specs/notes-management): "SHALL
      // remove it from the active list immediately and SHALL offer an
      // offline-capable undo action without requiring confirmation."
      final gateway = FakeGateway(unlocked: true);
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.text("Offline note"));
      await tester.pumpAndSettle();
      await tester.tap(find.byIcon(Icons.delete_forever_outlined));
      await tester.pumpAndSettle();

      expect(find.text("Delete this note?"), findsNothing);
      expect(gateway.note["deleted"], true);
      expect(find.byType(QuillEditor), findsNothing);
      expect(find.text("Offline note"), findsNothing);
      expect(find.text("Note deleted"), findsOneWidget);
      expect(find.widgetWithText(SnackBarAction, "Undo"), findsOneWidget);
    },
  );

  testWidgets(
    "restores a deleted note when Undo is tapped on the snackbar, even while offline",
    (tester) async {
      final gateway = FakeGateway(unlocked: true);
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.text("Offline note"));
      await tester.pumpAndSettle();
      await tester.tap(find.byIcon(Icons.delete_forever_outlined));
      await tester.pumpAndSettle();
      expect(gateway.note["deleted"], true);

      // FakeGateway.deleteNote(id, false) is the same local tombstone
      // toggle as any other commit - resolving here with no transport
      // call anywhere in this flow is exactly what "offline" looks like
      // for this scenario.
      await tester.tap(find.widgetWithText(SnackBarAction, "Undo"));
      await tester.pumpAndSettle();

      expect(gateway.note["deleted"], false);
      expect(find.text("Offline note"), findsOneWidget);
    },
  );

  testWidgets("background transition does not expose note text", (
    tester,
  ) async {
    final gateway = FakeGateway(unlocked: true);
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump();
    expect(find.text("Secret fixture body"), findsNothing);
  });

  testWidgets("sync status is visible and the server sheet reopens prefilled", (
    tester,
  ) async {
    final gateway =
        FakeGateway(unlocked: true)
          ..syncStatusValue = "current"
          ..connectionInfo = {
            "enabled": true,
            "url": "https://sync.example.com",
            "protocol": "https",
            "security_mode": "pinned",
            "fingerprint": "ab12",
          };
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();

    final cloudButton = tester.widget<IconButton>(
      find.widgetWithIcon(IconButton, Icons.cloud_done_outlined),
    );
    expect(cloudButton.tooltip, contains("Up to date"));

    await tester.tap(find.byIcon(Icons.cloud_done_outlined));
    await tester.pumpAndSettle();

    expect(find.text("Sync status: Up to date"), findsOneWidget);
    expect(find.text("Connection protocol"), findsOneWidget);
    expect(find.text("HTTPS / TLS 1.3"), findsOneWidget);
    expect(find.text("Certificate verification"), findsOneWidget);
    expect(find.text("Pinned certificate"), findsOneWidget);
    expect(find.widgetWithText(TextField, "HTTPS server URL"), findsNothing);

    await tester.ensureVisible(find.text("Advanced connection setup"));
    await tester.tap(find.text("Advanced connection setup"));
    await tester.pumpAndSettle();

    expect(find.widgetWithText(TextField, "HTTPS server URL"), findsOneWidget);
    final urlFinder = find.widgetWithText(TextField, "HTTPS server URL");
    final urlField = tester.widget<TextField>(urlFinder);
    expect(urlField.controller!.text, "https://sync.example.com");
    // The summary card above may have scrolled out of the list's cache
    // extent by now (a normal virtualized ListView, not a bug), so this
    // only asserts on the freshly revealed advanced section itself rather
    // than an exact count shared with a widget that can be unmounted.
    expect(find.text("Certificate verification"), findsWidgets);
    expect(find.text("Pinned certificate"), findsWidgets);
    expect(
      find.widgetWithText(FilledButton, "Apply server changes"),
      findsOneWidget,
    );

    await tester.enterText(urlFinder, "https://new.example.com");
    final trustedPolicy = find.text("System-trusted certificate");
    await tester.ensureVisible(trustedPolicy.last);
    await tester.tap(trustedPolicy.last);
    final applyButton = find.widgetWithText(
      FilledButton,
      "Apply server changes",
    );
    await tester.ensureVisible(applyButton);
    await tester.tap(applyButton);
    await tester.pumpAndSettle();

    expect(gateway.connectedConfig?["url"], "https://new.example.com");
    expect(gateway.connectedConfig?["security_mode"], "trusted");
  });

  testWidgets(
    "connects from a pasted connection code without exposing the advanced fields",
    (tester) async {
      final gateway =
          FakeGateway(unlocked: true)
            ..connectionInfo = {
              "enabled": true,
              "url": "https://code.example.com",
              "protocol": "https",
              "security_mode": "pinned",
              "fingerprint": "cd34",
            };
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.cloud_outlined));
      await tester.pumpAndSettle();

      expect(find.widgetWithText(TextField, "HTTPS server URL"), findsNothing);
      await tester.enterText(
        find.widgetWithText(TextField, "Connection code"),
        "beresta://connect?url=https://code.example.com&invite=abc&fingerprint=cd34&mode=pinned",
      );
      await tester.tap(find.widgetWithText(FilledButton, "Connect with code"));
      await tester.pumpAndSettle();

      expect(gateway.connectedConfig, {
        "url": "",
        "invite_code": "",
        "fingerprint": "",
        "security_mode": "",
        "qr_code":
            "beresta://connect?url=https://code.example.com&invite=abc&fingerprint=cd34&mode=pinned",
        "device_name": "Android",
      });
    },
  );

  testWidgets(
    "shares the workspace from a pasted identity code only after explicit confirmation, then shows a QR success state (task 7.7)",
    (tester) async {
      // A tall surface keeps every section of this long sheet already
      // built (a plain ListView only builds visible + cache-extent
      // children), so this test can assert on later sections without a
      // brittle sequence of scroll-then-tap steps.
      await tester.binding.setSurfaceSize(const Size(400, 3000));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      final gateway = FakeGateway(unlocked: true);
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.cloud_outlined));
      await tester.pumpAndSettle();

      await tester.enterText(
        find.widgetWithText(TextField, "Paste their identity code"),
        "beresta://identity?user=peer&key=00",
      );
      await tester.tap(find.byKey(const Key("share-continue-button")));
      await tester.pumpAndSettle();

      // Not shared yet: an explicit confirmation dialog sits between
      // pasting the code and actually granting access.
      expect(
        find.textContaining("They will be able to read and change"),
        findsOneWidget,
      );
      await tester.tap(
        find.widgetWithText(FilledButton, "Confirm and generate code"),
      );
      await tester.pumpAndSettle();

      expect(find.text("Ready to share."), findsOneWidget);
      expect(find.byType(QrImageView), findsWidgets);
      // The grant code text itself stays behind "Show code".
      expect(
        find.text("beresta://grant?workspace=fake&key=00&authority=00&sig=00"),
        findsNothing,
      );
      await tester.tap(find.byKey(const Key("grant-show-code-button")));
      await tester.pumpAndSettle();

      expect(
        find.text("beresta://grant?workspace=fake&key=00&authority=00&sig=00"),
        findsOneWidget,
      );
    },
  );

  testWidgets(
    "joins a shared workspace from a pasted grant code only after explicit confirmation, then shows a plain-language success state (task 7.7)",
    (tester) async {
      await tester.binding.setSurfaceSize(const Size(400, 3000));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      final gateway = FakeGateway(unlocked: true)..syncStatusValue = "current";
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      // "current" status renders as a filled cloud icon, not the default
      // outlined one - see syncStatusIcon in app.dart.
      await tester.tap(find.byIcon(Icons.cloud_done_outlined));
      await tester.pumpAndSettle();

      await tester.enterText(
        find.widgetWithText(TextField, "Paste their grant code"),
        "beresta://grant?workspace=w&key=k&authority=a&sig=s",
      );
      await tester.tap(find.byKey(const Key("join-continue-button")));
      await tester.pumpAndSettle();

      // Not joined yet: an explicit confirmation dialog sits between
      // pasting the code and actually joining.
      expect(find.text("Join this workspace?"), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, "Confirm and join"));
      await tester.pumpAndSettle();

      expect(find.text("You're connected."), findsOneWidget);
      expect(
        find.text("This device now has access to the shared workspace."),
        findsOneWidget,
      );

      await tester.tap(find.widgetWithText(FilledButton, "Close"));
      await tester.pumpAndSettle();

      // Dismissed the sheet instead of leaving the success state open.
      expect(find.text("You're connected."), findsNothing);
    },
  );

  testWidgets(
    "shows understandable device names, platform, last seen, and access state, hiding the raw device ID (task 7.8)",
    (tester) async {
      await tester.binding.setSurfaceSize(const Size(400, 3000));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      final gateway = FakeGateway(unlocked: true)
        ..syncDevicesToReturn = [
          {
            "device_id": "device-local",
            "display_name": "This device",
            "platform": "android",
            "created_at": "2026-01-01T00:00:00Z",
          },
          {
            "device_id": "device-remote",
            "display_name": "Kitchen tablet",
            "platform": "android",
            "created_at": "2026-01-01T00:00:00Z",
            "last_seen_at": "2026-02-03T04:05:00Z",
          },
          {
            "device_id": "device-old",
            "display_name": "Old laptop",
            "platform": "windows",
            "created_at": "2026-01-01T00:00:00Z",
            "revoked_at": "2026-02-01T00:00:00Z",
          },
        ];
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.cloud_outlined));
      await tester.pumpAndSettle();

      expect(find.text("This device"), findsOneWidget);
      expect(find.text("Local"), findsOneWidget);
      expect(find.text("Kitchen tablet"), findsOneWidget);
      expect(find.text("Old laptop"), findsOneWidget);
      expect(find.text("Revoked"), findsOneWidget);
      expect(
        find.widgetWithText(TextButton, "Disconnect"),
        findsOneWidget,
      );
      // The device_id itself is a protocol identifier and must not appear
      // in this primary list (specs/identity-and-sharing's "Understandable
      // device inventory").
      expect(find.text("device-remote"), findsNothing);
      expect(find.text("device-old"), findsNothing);
    },
  );

  testWidgets(
    "disconnects a device only after confirming the revocation limitation disclosure (task 7.8)",
    (tester) async {
      await tester.binding.setSurfaceSize(const Size(400, 3000));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      final gateway = FakeGateway(unlocked: true)
        ..syncDevicesToReturn = [
          {
            "device_id": "device-local",
            "display_name": "This device",
            "platform": "android",
            "created_at": "2026-01-01T00:00:00Z",
          },
          {
            "device_id": "device-remote",
            "display_name": "Kitchen tablet",
            "platform": "android",
            "created_at": "2026-01-01T00:00:00Z",
          },
        ];
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.cloud_outlined));
      await tester.pumpAndSettle();

      await tester.tap(find.widgetWithText(TextButton, "Disconnect"));
      await tester.pumpAndSettle();

      expect(gateway.revokedSyncDeviceIds, isEmpty);
      expect(
        find.textContaining("cannot erase notes or keys"),
        findsOneWidget,
      );

      await tester.tap(find.widgetWithText(FilledButton, "Disconnect"));
      await tester.pumpAndSettle();

      expect(gateway.revokedSyncDeviceIds, ["device-remote"]);
    },
  );

  testWidgets(
    "backup sheet shows the catalog's health, last verified time, and location",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..backupStatusValue = {
          "health": "healthy",
          "last_verified_unix_ms":
              DateTime.utc(2026, 1, 1).millisecondsSinceEpoch,
          "location": "/backups/daily-1",
        };
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.backup_outlined));
      await tester.pumpAndSettle();

      expect(find.text("Healthy"), findsOneWidget);
      expect(find.text("/backups/daily-1"), findsOneWidget);
    },
  );

  testWidgets(
    "backup sheet explains a corrupt backup catalog status in plain language",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..backupStatusValue = {
          "health": "corrupt",
          "last_verified_unix_ms": 0,
          "location": "",
        };
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.backup_outlined));
      await tester.pumpAndSettle();

      expect(find.text("Corrupt"), findsOneWidget);
      expect(
        find.text("This backup failed verification and cannot be restored."),
        findsOneWidget,
      );
    },
  );

  testWidgets(
    "restore options sheet plans a dry run and restores only the addition/update notes as new",
    (tester) async {
      final gateway =
          FakeGateway(unlocked: true)
            ..backupsValue = [
              {
                "id": "backup-1",
                "kind": 1,
                "created_unix_ms": 0,
                "corrupt": false,
              },
            ]
            ..previewBackupValue = {
              "note_titles": ["Note A", "Note B"],
            }
            ..planRestoreValue = {
              "entries": [
                {"note_id": "note-a", "title": "Note A", "kind": "addition"},
                {"note_id": "note-b", "title": "Note B", "kind": "unchanged"},
              ],
              "required_storage_bytes": 2048,
            }
            ..restoreSelectiveValue = {
              "safety_backup": <String, dynamic>{},
              "new_note_ids": ["note-a"],
            };
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.backup_outlined));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(TextButton, "Restore"));
      await tester.pumpAndSettle();

      expect(find.text("Note A"), findsOneWidget);
      expect(find.text("Note B"), findsOneWidget);

      await tester.tap(
        find.widgetWithText(FilledButton, "Restore selected as new notes"),
      );
      await tester.pumpAndSettle();

      // "unchanged" is not pre-selected, only "addition" is.
      final checkboxes = tester.widgetList<CheckboxListTile>(
        find.byType(CheckboxListTile),
      );
      expect(checkboxes.where((c) => c.value == true).length, 1);

      await tester.tap(
        find.widgetWithText(FilledButton, "Restore selected as new notes"),
      );
      await tester.pumpAndSettle();

      expect(gateway.lastRestoreSelectiveNoteIds, ["note-a"]);
      expect(find.text("Notes restored: 1"), findsOneWidget);
    },
  );

  testWidgets(
    "restore options sheet requires an extra confirmation before replacing everything",
    (tester) async {
      var restoredBackupId = "";
      final gateway = FakeGateway(unlocked: true)
        ..backupsValue = [
          {"id": "backup-1", "kind": 1, "created_unix_ms": 0, "corrupt": false},
        ];
      gateway.onRestoreBackup = (id) => restoredBackupId = id;
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.backup_outlined));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(TextButton, "Restore"));
      await tester.pumpAndSettle();
      await tester.tap(
        find.widgetWithText(OutlinedButton, "Replace from backup"),
      );
      await tester.pumpAndSettle();

      expect(restoredBackupId, "");
      expect(
        find.text(
          "This verifies the backup, creates a safety backup, and replaces the local collection.",
        ),
        findsOneWidget,
      );

      await tester.tap(find.widgetWithText(FilledButton, "Restore"));
      await tester.pumpAndSettle();

      expect(restoredBackupId, "backup-1");
      expect(find.text("Restore complete."), findsOneWidget);
    },
  );

  testWidgets(
    "backup sheet shows a storage-pressure estimate before backing up",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..estimateBackupSizeValue = 1500000;
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.backup_outlined));
      await tester.pumpAndSettle();

      expect(find.textContaining("Estimated size"), findsOneWidget);
    },
  );

  testWidgets(
    "backup sheet offers a change-destination remedy when backup fails from insufficient space",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)
        ..createBackupFailure = Exception(
          "not enough space at the backup destination",
        );
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      await tester.tap(find.byIcon(Icons.backup_outlined));
      await tester.pumpAndSettle();
      expect(
        find.widgetWithText(OutlinedButton, "Choose destination"),
        findsOneWidget,
      );

      await tester.tap(find.widgetWithText(FilledButton, "Back up now"));
      await tester.pumpAndSettle();

      expect(
        find.text("Not enough free space. Existing backups were not changed."),
        findsOneWidget,
      );
      // The top action row already has its own "Choose destination"
      // button; the remedy below the error message is a second one.
      final remedyButtons = find.widgetWithText(
        OutlinedButton,
        "Choose destination",
      );
      expect(remedyButtons, findsNWidgets(2));

      await tester.tap(remedyButtons.last);
      await tester.pumpAndSettle();

      expect(gateway.selectBackupDestinationCalls, 1);
    },
  );

  testWidgets("refreshes notes after the selected workspace finishes syncing", (
    tester,
  ) async {
    final gateway = FakeGateway(unlocked: true)..listedNotes = [];
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();

    gateway.listedNotes = [
      {...gateway.note, "title": "Shared from desktop"},
    ];
    gateway.events.add({"sequence": 1, "type": "workspace_synced"});
    await tester.pump(const Duration(seconds: 1));
    await tester.pumpAndSettle();

    expect(find.text("Shared from desktop"), findsOneWidget);
  });

  testWidgets("sync button refreshes the current workspace collection", (
    tester,
  ) async {
    final gateway =
        FakeGateway(unlocked: true)
          ..listedNotes = []
          ..syncStatusValue = "current";
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();

    gateway.listedNotes = [
      {...gateway.note, "title": "Downloaded after refresh"},
    ];
    final syncCallsBeforeTap = gateway.syncNowCalls;
    await tester.tap(find.byIcon(Icons.sync));
    await tester.pumpAndSettle();

    expect(gateway.syncNowCalls, syncCallsBeforeTap + 1);
    expect(find.text("Downloaded after refresh"), findsOneWidget);
  });

  testWidgets("syncs when the app opens and moves to the background", (
    tester,
  ) async {
    final gateway = FakeGateway(unlocked: true);
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();
    final syncCallsAfterOpen = gateway.syncNowCalls;

    expect(syncCallsAfterOpen, greaterThanOrEqualTo(1));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump();

    expect(gateway.syncNowCalls, greaterThan(syncCallsAfterOpen));
  });

  testWidgets(
    "background auto-lock waits for the configured duration instead of a hardcoded 5 minutes",
    (tester) async {
      final gateway =
          FakeGateway(unlocked: true)..autoLockMinutesValue = 15;
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      // Lets the in-flight getSettings() call (a microtask, not a Timer)
      // resolve and arm lockTimer with the real configured duration.
      await tester.pump();
      await tester.pump();

      // Still well short of the configured 15 minutes: the account must
      // still be unlocked, not locked after the old hardcoded 5 minutes.
      await tester.pump(const Duration(minutes: 6));
      expect(gateway.unlocked, isTrue);

      await tester.pump(const Duration(minutes: 10));
      expect(gateway.unlocked, isFalse);
    },
  );

  testWidgets(
    "background auto-lock locks immediately when auto_lock_minutes is 0",
    (tester) async {
      final gateway = FakeGateway(unlocked: true)..autoLockMinutesValue = 0;
      await tester.pumpWidget(BerestaApp(gateway: gateway));
      await tester.pumpAndSettle();

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await tester.pump();
      await tester.pump();

      expect(gateway.unlocked, isFalse);
    },
  );
}

class FakeGateway implements CoreGateway {
  FakeGateway({required this.unlocked});

  bool unlocked;
  bool accountExists = false;
  bool deviceUnlockAvailable = false;
  String deviceId = "device-local";
  int deviceUnlockCalls = 0;
  Object? deviceUnlockError;
  String savedBody = "";
  String createdNoteTitle = "";
  String createdNoteNotebookId = "";
  (String, String)? removedAttachment;
  List<Map<String, dynamic>> attachmentList = [];
  String syncStatusValue = "local_only";
  int syncNowCalls = 0;
  late List<Map<String, dynamic>> listedNotes = [note];
  final events = <Map<String, dynamic>>[];
  Map<String, dynamic> connectionInfo = {
    "enabled": false,
    "url": "",
    "security_mode": "pinned",
    "fingerprint": "",
  };
  static const noteFixture = <String, dynamic>{
    "id": "018f0000-0000-7000-8000-000000000001",
    "workspace_id": "018f0000-0000-7000-8000-000000000002",
    "notebook_id": "",
    "title": "Offline note",
    "pinned": false,
    "archived": false,
    "deleted": false,
    "created_unix_ms": 1710000000000,
    "updated_unix_ms": 1710000000000,
  };
  final note = Map<String, dynamic>.from(noteFixture);

  @override
  Future<Map<String, dynamic>> status() async => {
    "unlocked": unlocked,
    "device_id": deviceId,
    "account_exists": accountExists,
    "device_unlock_available": deviceUnlockAvailable,
  };
  @override
  Future<Map<String, dynamic>> createAccount(String passphrase) async {
    unlocked = true;
    return {};
  }

  @override
  Future<Map<String, dynamic>> unlockAccount(String passphrase) async {
    unlocked = true;
    return {};
  }

  @override
  Future<Map<String, dynamic>> unlockWithDeviceAuthentication() async {
    deviceUnlockCalls++;
    if (deviceUnlockError != null) throw deviceUnlockError!;
    unlocked = true;
    return {};
  }

  @override
  Future<void> lock() async => unlocked = false;
  @override
  Future<List<Map<String, dynamic>>> listNotes() async => listedNotes;
  @override
  Future<Map<String, dynamic>> createNote(
    String title, {
    String notebookId = "",
  }) async {
    createdNoteTitle = title;
    createdNoteNotebookId = notebookId;
    return note;
  }

  // A fake stand-in for core/mobileapi's real base_revision (task 6.8):
  // getNote hands out the current value, saveNoteCancelable mints and
  // returns a new one on every successful save. A test asserting on
  // ordering/threading (not real CRDT merge semantics, which the Go layer
  // already covers) only needs these to be distinct and traceable, not
  // structurally meaningful.
  String noteBaseRevision = "base-0";
  int _baseRevisionCounter = 0;
  // baseRevisions saveNoteCancelable has actually been called with, in
  // call order - lets a test assert that commit() threaded the right
  // (possibly just-updated) base_revision into each save.
  final List<String> saveBaseRevisions = [];

  @override
  Future<Map<String, dynamic>> getNote(String id) async => {
    "note": note,
    "body": savedBody,
    "base_revision": noteBaseRevision,
  };
  // requestIds saveNoteCancelable has been called with, in call order -
  // lets a test assert on commit/cancellation ordering.
  final List<String> saveRequestIds = [];
  final Set<String> canceledRequestIds = {};
  // When non-null, saveNoteCancelable awaits this before resolving,
  // letting a test hold a commit "in flight" to exercise cancellation and
  // out-of-order completion, matching core/mobileapi's real behavior of
  // not completing a request until its context is done or the work
  // finishes.
  Completer<void>? holdSaveNote;
  Object? nextSaveNoteFailure;

  @override
  Future<String> saveNoteCancelable(
    String requestId,
    String id,
    String title,
    String body,
    String baseRevision,
  ) async {
    saveRequestIds.add(requestId);
    saveBaseRevisions.add(baseRevision);
    final hold = holdSaveNote;
    if (hold != null) await hold.future;
    if (canceledRequestIds.contains(requestId)) {
      throw StateError("canceled: $requestId");
    }
    final failure = nextSaveNoteFailure;
    if (failure != null) {
      nextSaveNoteFailure = null;
      throw failure;
    }
    savedBody = body;
    noteBaseRevision = "base-${++_baseRevisionCounter}";
    return noteBaseRevision;
  }

  @override
  Future<void> cancelRequest(String requestId) async {
    canceledRequestIds.add(requestId);
  }

  @override
  Future<void> deleteNote(String id, bool deleted) async =>
      note["deleted"] = deleted;
  @override
  Future<void> moveNote(String id, String notebookId) async =>
      note["notebook_id"] = notebookId;
  int searchCalls = 0;
  @override
  Future<List<Map<String, dynamic>>> search(String query) async {
    searchCalls++;
    return [note];
  }

  @override
  Future<Map<String, dynamic>> createNotebook(
    String name, {
    String parentId = "",
  }) async => {"id": "018f0000-0000-7000-8000-000000000003", "name": name};
  List<Map<String, dynamic>> notebooksList = [];
  @override
  Future<List<Map<String, dynamic>>> listNotebooks() async => notebooksList;
  (String, String)? renamedNotebook;
  @override
  Future<void> renameNotebook(String id, String name) async {
    renamedNotebook = (id, name);
    final notebook = notebooksList.firstWhere((item) => item["id"] == id);
    notebook["name"] = name;
  }

  @override
  Future<void> deleteNotebook(String id, bool deleted) async {}
  @override
  Future<List<Map<String, dynamic>>> listNoteAttachments(String noteId) async =>
      attachmentList;
  @override
  Future<Uint8List> readAttachmentData(String blobId) async => Uint8List(0);
  @override
  Future<void> removeAttachmentData(String noteId, String blobId) async {
    removedAttachment = (noteId, blobId);
    attachmentList =
        attachmentList
            .where((attachment) => attachment["blob_id"] != blobId)
            .toList();
  }

  @override
  Future<List<Map<String, dynamic>>> listTags() async => [];
  @override
  Future<Map<String, dynamic>> createTag(String name) async => {
    "id": "018f0000-0000-7000-8000-000000000004",
    "name": name,
  };
  @override
  Future<void> deleteTag(String id, bool deleted) async {}
  @override
  Future<void> setNoteTag(String noteId, String tagId, bool present) async {}
  @override
  Future<List<String>> listNoteTags(String noteId) async => [];
  // Oldest first, matching the real gateway's contract.
  List<Map<String, dynamic>> revisionList = [];
  String? restoredRevisionId;
  @override
  Future<List<Map<String, dynamic>>> listRevisions(String noteId) async =>
      revisionList;
  @override
  Future<List<Map<String, dynamic>>> diffRevisions(
    String noteId,
    String fromRevisionId,
    String toRevisionId,
  ) async => [
    {"op": "delete", "text": "from $fromRevisionId"},
    {"op": "insert", "text": "to $toRevisionId"},
  ];
  @override
  Future<void> restoreRevision(String noteId, String revisionId) async =>
      restoredRevisionId = revisionId;
  @override
  Future<void> syncNow() async => syncNowCalls++;
  Map<String, dynamic>? connectedConfig;
  @override
  Future<void> connectServer(Map<String, dynamic> config) async {
    connectedConfig = config;
  }

  @override
  Future<void> disconnectServer() async {}
  @override
  Future<Map<String, dynamic>> syncSummary() async => {
    "state": syncStatusValue,
    "pending_count": 0,
    "last_success_unix_ms": 0,
    "retry_in_ms": 0,
    "unsafe_count": 0,
    "action_required": "none",
  };
  List<Map<String, dynamic>> quarantineEntries = [];
  @override
  Future<List<Map<String, dynamic>>> listSyncQuarantine() async =>
      quarantineEntries;
  @override
  Future<void> retryQuarantined(String operationId) async {
    quarantineEntries =
        quarantineEntries
            .where((entry) => entry["operation_id"] != operationId)
            .toList();
  }

  @override
  Future<Map<String, dynamic>> syncConnectionInfo() async => connectionInfo;
  @override
  Future<String> exportIdentity() async =>
      "beresta://identity?user=fake&key=00";
  @override
  Future<String> shareWorkspace(String identityCode) async =>
      "beresta://grant?workspace=fake&key=00&authority=00&sig=00";
  @override
  Future<Map<String, dynamic>> acceptWorkspaceGrant(String grantCode) async => {
    "workspace_id": "fake",
    "role": "member",
    "active": true,
  };
  List<Map<String, dynamic>> workspacesToReturn = [];
  void Function(String workspaceId)? onSetActiveWorkspace;
  @override
  Future<List<Map<String, dynamic>>> listWorkspaces() async =>
      workspacesToReturn;
  @override
  Future<void> setActiveWorkspace(String workspaceId) async {
    onSetActiveWorkspace?.call(workspaceId);
  }

  List<Map<String, dynamic>> syncDevicesToReturn = [];
  final List<String> revokedSyncDeviceIds = [];
  @override
  Future<List<Map<String, dynamic>>> listSyncDevices() async =>
      syncDevicesToReturn;
  @override
  Future<void> revokeSyncDevice(String deviceId) async {
    revokedSyncDeviceIds.add(deviceId);
  }

  int capturePhotoCalls = 0;
  Object? capturePhotoFailure;
  Completer<void>? capturePhotoCompleter;
  @override
  Future<void> capturePhoto(String noteId) async {
    capturePhotoCalls += 1;
    final completer = capturePhotoCompleter;
    if (completer != null) await completer.future;
    final failure = capturePhotoFailure;
    if (failure != null) throw failure;
  }

  int selectBackupDestinationCalls = 0;
  @override
  Future<bool> selectBackupDestination() async {
    selectBackupDestinationCalls += 1;
    return false;
  }

  Object? createBackupFailure;
  @override
  Future<void> createBackup() async {
    if (createBackupFailure != null) throw createBackupFailure!;
  }

  int estimateBackupSizeValue = 0;
  @override
  Future<int> estimateBackupSize() async => estimateBackupSizeValue;
  List<Map<String, dynamic>> backupsValue = [];
  @override
  Future<List<Map<String, dynamic>>> listBackups() async => backupsValue;
  Map<String, dynamic> backupStatusValue = {
    "health": "unknown",
    "last_verified_unix_ms": 0,
    "location": "",
  };
  @override
  Future<Map<String, dynamic>> backupStatus() async => backupStatusValue;
  Map<String, dynamic> previewBackupValue = {"note_titles": <String>[]};
  @override
  Future<Map<String, dynamic>> previewBackup(String backupId) async =>
      previewBackupValue;
  Map<String, dynamic> planRestoreValue = {
    "entries": <Map<String, dynamic>>[],
    "required_storage_bytes": 0,
  };
  List<String>? lastPlanRestoreNoteIds;
  @override
  Future<Map<String, dynamic>> planRestore(
    String backupId,
    List<String> noteIds,
  ) async {
    lastPlanRestoreNoteIds = noteIds;
    return planRestoreValue;
  }

  Map<String, dynamic> restoreSelectiveValue = {
    "safety_backup": <String, dynamic>{},
    "new_note_ids": <String>[],
  };
  List<String>? lastRestoreSelectiveNoteIds;
  @override
  Future<Map<String, dynamic>> restoreSelective(
    String backupId,
    List<String> noteIds,
  ) async {
    lastRestoreSelectiveNoteIds = noteIds;
    return restoreSelectiveValue;
  }

  void Function(String backupId)? onRestoreBackup;
  @override
  Future<void> restoreBackup(String backupId) async {
    onRestoreBackup?.call(backupId);
  }

  @override
  Future<int> importBackups() async => 0;
  int autoLockMinutesValue = 5;
  @override
  Future<Map<String, dynamic>> getSettings() async => {
    "language": "en",
    "auto_lock_minutes": autoLockMinutesValue,
    "backup_destination": "",
    "attachment_retention": "all",
    "selected_notebooks": <String>[],
    "cache_limit_bytes": 536870912,
  };
  @override
  Future<void> updateSettings(Map<String, dynamic> settings) async {}
  @override
  Future<List<Map<String, dynamic>>> pollEvents(int afterSequence) async =>
      events
          .where((event) => (event["sequence"] as int) > afterSequence)
          .toList();

  Map<String, dynamic> diagnosticSummaryValue = {
    "app_version": "0.1.0",
    "platform": "android",
    "sync_configured": false,
    "last_successful_sync_unix_ms": 0,
    "pending_count": 0,
    "connection_state": "local_only",
    "backup": {"health": "unknown", "last_verified_unix_ms": 0, "location": ""},
    "storage_usage_bytes": 0,
    "database": "ok",
    "update": "unknown",
  };
  Map<String, dynamic> technicalDiagnosticsValue = {
    "workspace_id": "",
    "device_id": "",
    "last_error_class": "",
    "pending_operation_count": 0,
    "quarantined_operation_ids": <String>[],
    "cursor_sequence": 0,
    "cursor_epoch": 0,
    "retry_count": 0,
    "retry_in_ms": 0,
    "transport_protocol": "",
    "transport_security_mode": "",
    "transport_url": "",
    "migration_version": 0,
  };
  String copyDiagnosticsValue = "Beresta diagnostics\n";
  @override
  Future<Map<String, dynamic>> diagnosticSummary(String appVersion) async =>
      diagnosticSummaryValue;
  @override
  Future<Map<String, dynamic>> technicalDiagnostics() async =>
      technicalDiagnosticsValue;
  @override
  Future<String> copyDiagnostics(String appVersion) async =>
      copyDiagnosticsValue;
}

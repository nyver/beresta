import "dart:async";
import "dart:typed_data";

import "package:beresta/main.dart";
import "package:flutter/material.dart";
import "package:flutter_quill/flutter_quill.dart";
import "package:flutter_test/flutter_test.dart";

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
      await tester.tap(find.text("Revisions"));
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
    expect(find.widgetWithText(TextField, "HTTPS server URL"), findsOneWidget);
    final urlFinder = find.widgetWithText(TextField, "HTTPS server URL");
    final urlField = tester.widget<TextField>(urlFinder);
    expect(urlField.controller!.text, "https://sync.example.com");
    expect(find.text("Connection protocol"), findsOneWidget);
    expect(find.text("HTTPS / TLS 1.3"), findsOneWidget);
    expect(find.text("Certificate verification"), findsNWidgets(2));
    expect(find.text("Pinned certificate"), findsNWidgets(2));
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
}

class FakeGateway implements CoreGateway {
  FakeGateway({required this.unlocked});

  bool unlocked;
  bool accountExists = false;
  bool deviceUnlockAvailable = false;
  int deviceUnlockCalls = 0;
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

  @override
  Future<Map<String, dynamic>> getNote(String id) async => {
    "note": note,
    "body": savedBody,
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
  Future<void> saveNoteCancelable(
    String requestId,
    String id,
    String title,
    String body,
  ) async {
    saveRequestIds.add(requestId);
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
    quarantineEntries = quarantineEntries
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

  @override
  Future<void> capturePhoto(String noteId) async {}
  @override
  Future<bool> selectBackupDestination() async => false;
  @override
  Future<void> createBackup() async {}
  @override
  Future<List<Map<String, dynamic>>> listBackups() async => [];
  @override
  Future<Map<String, dynamic>> previewBackup(String backupId) async => {};
  @override
  Future<void> restoreBackup(String backupId) async {}
  @override
  Future<int> importBackups() async => 0;
  @override
  Future<Map<String, dynamic>> getSettings() async => {
    "language": "en",
    "auto_lock_minutes": 5,
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
}

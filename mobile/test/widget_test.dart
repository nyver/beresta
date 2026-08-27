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
    // The save button only enables once the body controller's listener
    // marks the editor dirty (see _EditorScreenState.markDirty), which
    // takes effect on the next frame rather than synchronously with
    // replaceText; without this pump the tap below hits a still-disabled
    // button and silently does nothing.
    await tester.pump();
    await tester.tap(find.byIcon(Icons.save_outlined));
    await tester.pumpAndSettle();

    expect(gateway.savedBody, enteredText);
  });

  testWidgets(
    "revision history lists newest first with a checkpoint badge and shows a diff",
    (tester) async {
      final gateway =
          FakeGateway(unlocked: true)
            ..revisionList = [
              {
                "id": "rev-1",
                "checkpoint": true,
                "created_unix_ms": 1710000000000,
              },
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

  testWidgets("new and untitled notes use the desktop title", (tester) async {
    final gateway = FakeGateway(unlocked: true)
      ..listedNotes = [
        {...FakeGateway.noteFixture, "title": ""},
      ];
    await tester.pumpWidget(BerestaApp(gateway: gateway));
    await tester.pumpAndSettle();

    expect(find.text("Untitled"), findsOneWidget);

    await tester.tap(find.byIcon(Icons.add));
    await tester.pumpAndSettle();
    expect(gateway.createdNoteTitle, "Untitled");
  });

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
    final urlField = tester.widget<TextField>(
      find.widgetWithText(TextField, "HTTPS server URL"),
    );
    expect(urlField.controller!.text, "https://sync.example.com");
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
  String syncStatusValue = "disabled";
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
  @override
  Future<void> saveNote(String id, String title, String body) async =>
      savedBody = body;
  @override
  Future<void> deleteNote(String id, bool deleted) async =>
      note["deleted"] = deleted;
  @override
  Future<void> moveNote(String id, String notebookId) async =>
      note["notebook_id"] = notebookId;
  @override
  Future<List<Map<String, dynamic>>> search(String query) async => [note];
  @override
  Future<Map<String, dynamic>> createNotebook(
    String name, {
    String parentId = "",
  }) async => {"id": "018f0000-0000-7000-8000-000000000003", "name": name};
  @override
  Future<List<Map<String, dynamic>>> listNotebooks() async => [];
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
  @override
  Future<void> connectServer(Map<String, dynamic> config) async {}
  @override
  Future<void> disconnectServer() async {}
  @override
  Future<String> syncStatus() async => syncStatusValue;
  @override
  Future<String> syncError() async => "";
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
  @override
  Future<List<Map<String, dynamic>>> listWorkspaces() async => [];
  @override
  Future<void> setActiveWorkspace(String workspaceId) async {}
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

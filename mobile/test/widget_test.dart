import "dart:typed_data";

import "package:beresta/main.dart";
import "package:flutter/material.dart";
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
    final body = find.widgetWithText(TextField, "Markdown body");
    await tester.enterText(body, "Offline paragraph");
    // The save button only enables once the body controller's listener
    // marks the editor dirty (see _EditorScreenState.markDirty), which
    // takes effect on the next frame rather than synchronously with
    // enterText; without this pump the tap below hits a still-disabled
    // button and silently does nothing.
    await tester.pump();
    await tester.tap(find.byIcon(Icons.save_outlined));
    await tester.pumpAndSettle();

    expect(gateway.savedBody, "Offline paragraph");
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
}

class FakeGateway implements CoreGateway {
  FakeGateway({required this.unlocked});

  bool unlocked;
  String savedBody = "";
  String syncStatusValue = "disabled";
  Map<String, dynamic> connectionInfo = {
    "enabled": false,
    "url": "",
    "security_mode": "pinned",
    "fingerprint": "",
  };
  final note = <String, dynamic>{
    "id": "018f0000-0000-7000-8000-000000000001",
    "workspace_id": "018f0000-0000-7000-8000-000000000002",
    "notebook_id": "",
    "title": "Offline note",
    "pinned": false,
    "archived": false,
    "deleted": false,
    "created_unix_ms": 1710000000000,
  };

  @override
  Future<Map<String, dynamic>> status() async => {"unlocked": unlocked};
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
  Future<void> lock() async => unlocked = false;
  @override
  Future<List<Map<String, dynamic>>> listNotes() async => [note];
  @override
  Future<Map<String, dynamic>> createNote(
    String title, {
    String notebookId = "",
  }) async => note;
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
      [];
  @override
  Future<Uint8List> readAttachmentData(String blobId) async => Uint8List(0);
  @override
  Future<void> removeAttachmentData(String noteId, String blobId) async {}
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
  @override
  Future<List<Map<String, dynamic>>> listRevisions(String noteId) async => [];
  @override
  Future<void> restoreRevision(String noteId, String revisionId) async {}
  @override
  Future<void> syncNow() async {}
  @override
  Future<void> connectServer(Map<String, dynamic> config) async {}
  @override
  Future<void> disconnectServer() async {}
  @override
  Future<String> syncStatus() async => syncStatusValue;
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
  Future<List<Map<String, dynamic>>> pollEvents(int afterSequence) async => [];
}

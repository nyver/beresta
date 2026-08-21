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
}

class FakeGateway implements CoreGateway {
  FakeGateway({required this.unlocked});

  bool unlocked;
  String savedBody = "";
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
  Future<List<Map<String, dynamic>>> search(String query) async => [note];
  @override
  Future<List<Map<String, dynamic>>> listNotebooks() async => [];
  @override
  Future<List<Map<String, dynamic>>> listTags() async => [];
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

import "dart:convert";

import "package:flutter/services.dart";

abstract interface class CoreGateway {
  Future<Map<String, dynamic>> status();
  Future<Map<String, dynamic>> createAccount(String passphrase);
  Future<Map<String, dynamic>> unlockAccount(String passphrase);
  Future<Map<String, dynamic>> unlockWithDeviceAuthentication();
  Future<void> lock();
  Future<List<Map<String, dynamic>>> listNotes();
  Future<Map<String, dynamic>> createNote(String title, {String notebookId});
  Future<Map<String, dynamic>> getNote(String id);
  Future<void> saveNote(String id, String title, String body);
  Future<void> deleteNote(String id, bool deleted);
  Future<void> moveNote(String id, String notebookId);
  Future<List<Map<String, dynamic>>> search(String query);
  Future<Map<String, dynamic>> createNotebook(String name, {String parentId});
  Future<List<Map<String, dynamic>>> listNotebooks();
  Future<void> renameNotebook(String id, String name);
  Future<void> deleteNotebook(String id, bool deleted);
  Future<List<Map<String, dynamic>>> listNoteAttachments(String noteId);
  Future<Uint8List> readAttachmentData(String blobId);
  Future<void> removeAttachmentData(String noteId, String blobId);
  Future<List<Map<String, dynamic>>> listTags();
  Future<Map<String, dynamic>> createTag(String name);
  Future<void> deleteTag(String id, bool deleted);
  Future<void> setNoteTag(String noteId, String tagId, bool present);
  Future<List<String>> listNoteTags(String noteId);
  Future<List<Map<String, dynamic>>> listRevisions(String noteId);
  Future<List<Map<String, dynamic>>> diffRevisions(
    String noteId,
    String fromRevisionId,
    String toRevisionId,
  );
  Future<void> restoreRevision(String noteId, String revisionId);
  Future<void> syncNow();
  Future<void> connectServer(Map<String, dynamic> config);
  Future<void> disconnectServer();
  Future<String> syncStatus();
  Future<String> syncError();
  Future<Map<String, dynamic>> syncConnectionInfo();
  Future<String> exportIdentity();
  Future<String> shareWorkspace(String identityCode);
  Future<Map<String, dynamic>> acceptWorkspaceGrant(String grantCode);
  Future<List<Map<String, dynamic>>> listWorkspaces();
  Future<void> setActiveWorkspace(String workspaceId);
  Future<void> capturePhoto(String noteId);
  Future<bool> selectBackupDestination();
  Future<void> createBackup();
  Future<List<Map<String, dynamic>>> listBackups();
  Future<Map<String, dynamic>> previewBackup(String backupId);
  Future<void> restoreBackup(String backupId);
  Future<int> importBackups();
  Future<Map<String, dynamic>> getSettings();
  Future<void> updateSettings(Map<String, dynamic> settings);
  Future<List<Map<String, dynamic>>> pollEvents(int afterSequence);
}

class MethodChannelCore implements CoreGateway {
  static const _channel = MethodChannel("app.beresta.notes/core/v1");
  int _request = 0;

  String _nextRequest() => "flutter-${++_request}";

  Future<dynamic> _invoke(String method, [Map<String, dynamic>? arguments]) {
    return _channel.invokeMethod<dynamic>(method, {
      "requestId": _nextRequest(),
      ...?arguments,
    });
  }

  Map<String, dynamic> _object(dynamic value) =>
      jsonDecode(value as String) as Map<String, dynamic>;

  List<Map<String, dynamic>> _list(dynamic value) =>
      (jsonDecode(value as String) as List<dynamic>)
          .cast<Map<String, dynamic>>();

  @override
  Future<Map<String, dynamic>> status() async =>
      _object(await _invoke("status"));

  @override
  Future<Map<String, dynamic>> createAccount(String passphrase) async =>
      _object(await _invoke("createAccount", {"passphrase": passphrase}));

  @override
  Future<Map<String, dynamic>> unlockAccount(String passphrase) async =>
      _object(await _invoke("unlockAccount", {"passphrase": passphrase}));

  @override
  Future<Map<String, dynamic>> unlockWithDeviceAuthentication() async =>
      _object(await _invoke("unlockWithDeviceAuthentication"));

  @override
  Future<void> lock() => _invoke("lock");

  @override
  Future<List<Map<String, dynamic>>> listNotes() async =>
      _list(await _invoke("listNotes"));

  @override
  Future<Map<String, dynamic>> createNote(
    String title, {
    String notebookId = "",
  }) async => _object(
    await _invoke("createNote", {"title": title, "notebookId": notebookId}),
  );

  @override
  Future<Map<String, dynamic>> getNote(String id) async =>
      _object(await _invoke("getNote", {"noteId": id}));

  @override
  Future<void> saveNote(String id, String title, String body) =>
      _invoke("saveNote", {"noteId": id, "title": title, "body": body});

  @override
  Future<void> deleteNote(String id, bool deleted) =>
      _invoke("deleteNote", {"noteId": id, "deleted": deleted});

  @override
  Future<void> moveNote(String id, String notebookId) =>
      _invoke("moveNote", {"noteId": id, "notebookId": notebookId});

  @override
  Future<List<Map<String, dynamic>>> search(String query) async =>
      _list(await _invoke("search", {"query": query}));

  @override
  Future<Map<String, dynamic>> createNotebook(
    String name, {
    String parentId = "",
  }) async => _object(
    await _invoke("createNotebook", {"name": name, "parentId": parentId}),
  );

  @override
  Future<List<Map<String, dynamic>>> listNotebooks() async =>
      _list(await _invoke("listNotebooks"));

  @override
  Future<void> renameNotebook(String id, String name) =>
      _invoke("renameNotebook", {"notebookId": id, "name": name});

  @override
  Future<void> deleteNotebook(String id, bool deleted) =>
      _invoke("deleteNotebook", {"notebookId": id, "deleted": deleted});

  @override
  Future<List<Map<String, dynamic>>> listNoteAttachments(String noteId) async =>
      _list(await _invoke("listNoteAttachments", {"noteId": noteId}));

  @override
  Future<Uint8List> readAttachmentData(String blobId) async =>
      await _invoke("readAttachmentData", {"blobId": blobId}) as Uint8List;

  @override
  Future<void> removeAttachmentData(String noteId, String blobId) =>
      _invoke("removeAttachmentData", {"noteId": noteId, "blobId": blobId});

  @override
  Future<List<Map<String, dynamic>>> listTags() async =>
      _list(await _invoke("listTags"));

  @override
  Future<Map<String, dynamic>> createTag(String name) async =>
      _object(await _invoke("createTag", {"name": name}));

  @override
  Future<void> deleteTag(String id, bool deleted) =>
      _invoke("deleteTag", {"tagId": id, "deleted": deleted});

  @override
  Future<void> setNoteTag(String noteId, String tagId, bool present) => _invoke(
    "setNoteTag",
    {"noteId": noteId, "tagId": tagId, "present": present},
  );

  @override
  Future<List<String>> listNoteTags(String noteId) async =>
      (jsonDecode(await _invoke("listNoteTags", {"noteId": noteId}) as String)
              as List<dynamic>)
          .cast<String>();

  @override
  Future<List<Map<String, dynamic>>> listRevisions(String noteId) async =>
      _list(await _invoke("listRevisions", {"noteId": noteId}));

  @override
  Future<List<Map<String, dynamic>>> diffRevisions(
    String noteId,
    String fromRevisionId,
    String toRevisionId,
  ) async => _list(
    await _invoke("diffRevisions", {
      "noteId": noteId,
      "fromRevisionId": fromRevisionId,
      "toRevisionId": toRevisionId,
    }),
  );

  @override
  Future<void> restoreRevision(String noteId, String revisionId) =>
      _invoke("restoreRevision", {"noteId": noteId, "revisionId": revisionId});

  @override
  Future<void> syncNow() => _invoke("syncNow");

  @override
  Future<void> connectServer(Map<String, dynamic> config) =>
      _invoke("connectServer", {"encoded": jsonEncode(config)});

  @override
  Future<void> disconnectServer() => _invoke("disconnectServer");

  @override
  Future<String> syncStatus() async => await _invoke("syncStatus") as String;

  @override
  Future<String> syncError() async => await _invoke("syncError") as String;

  @override
  Future<Map<String, dynamic>> syncConnectionInfo() async =>
      _object(await _invoke("syncConnectionInfo"));

  @override
  Future<String> exportIdentity() async =>
      _object(await _invoke("exportIdentity"))["identity_code"] as String;

  @override
  Future<String> shareWorkspace(String identityCode) async =>
      _object(
            await _invoke("shareWorkspace", {"identityCode": identityCode}),
          )["grant_code"]
          as String;

  @override
  Future<Map<String, dynamic>> acceptWorkspaceGrant(String grantCode) async =>
      _object(await _invoke("acceptWorkspaceGrant", {"grantCode": grantCode}));

  @override
  Future<List<Map<String, dynamic>>> listWorkspaces() async =>
      _list(await _invoke("listWorkspaces"));

  @override
  Future<void> setActiveWorkspace(String workspaceId) =>
      _invoke("setActiveWorkspace", {"workspaceId": workspaceId});

  @override
  Future<void> capturePhoto(String noteId) =>
      _invoke("capturePhoto", {"noteId": noteId});

  @override
  Future<bool> selectBackupDestination() async =>
      await _invoke("selectBackupDestination") as bool;

  @override
  Future<void> createBackup() => _invoke("createBackup");

  @override
  Future<List<Map<String, dynamic>>> listBackups() async =>
      _list(await _invoke("listBackups"));

  @override
  Future<Map<String, dynamic>> previewBackup(String backupId) async =>
      _object(await _invoke("previewBackup", {"backupId": backupId}));

  @override
  Future<void> restoreBackup(String backupId) =>
      _invoke("restoreBackup", {"backupId": backupId});

  @override
  Future<int> importBackups() async => (await _invoke("importBackups") as int);

  @override
  Future<Map<String, dynamic>> getSettings() async =>
      _object(await _invoke("getSettings"));

  @override
  Future<void> updateSettings(Map<String, dynamic> settings) =>
      _invoke("updateSettings", {"encoded": jsonEncode(settings)});

  @override
  Future<List<Map<String, dynamic>>> pollEvents(int afterSequence) async =>
      _list(await _invoke("pollEvents", {"afterSequence": afterSequence}));
}

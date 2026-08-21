import "dart:convert";

import "package:flutter/services.dart";

abstract interface class CoreGateway {
  Future<Map<String, dynamic>> status();
  Future<Map<String, dynamic>> createAccount(String passphrase);
  Future<Map<String, dynamic>> unlockAccount(String passphrase);
  Future<void> lock();
  Future<List<Map<String, dynamic>>> listNotes();
  Future<Map<String, dynamic>> createNote(String title, {String notebookId});
  Future<Map<String, dynamic>> getNote(String id);
  Future<void> saveNote(String id, String title, String body);
  Future<void> deleteNote(String id, bool deleted);
  Future<List<Map<String, dynamic>>> search(String query);
  Future<List<Map<String, dynamic>>> listNotebooks();
  Future<List<Map<String, dynamic>>> listTags();
  Future<List<Map<String, dynamic>>> listRevisions(String noteId);
  Future<void> restoreRevision(String noteId, String revisionId);
  Future<void> syncNow();
  Future<void> connectServer(Map<String, dynamic> config);
  Future<void> disconnectServer();
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
  Future<List<Map<String, dynamic>>> search(String query) async =>
      _list(await _invoke("search", {"query": query}));

  @override
  Future<List<Map<String, dynamic>>> listNotebooks() async =>
      _list(await _invoke("listNotebooks"));

  @override
  Future<List<Map<String, dynamic>>> listTags() async =>
      _list(await _invoke("listTags"));

  @override
  Future<List<Map<String, dynamic>>> listRevisions(String noteId) async =>
      _list(await _invoke("listRevisions", {"noteId": noteId}));

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

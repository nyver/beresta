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

  /// Commits a note body update identified by [requestId], matching
  /// core/mobileapi.Service's begin/Cancel request-tracking contract. A
  /// caller that starts a newer commit before this one resolves should
  /// cancel this one first via [cancelRequest]: SaveNote replaces the
  /// note's entire body from a fresh read of its current state, so two
  /// overlapping calls racing against the same base state can otherwise
  /// silently lose one of the two edits to CRDT merge order, not just
  /// misreport which one is "saved".
  Future<void> saveNoteCancelable(
    String requestId,
    String id,
    String title,
    String body,
  );

  /// Cancels an in-flight request started by [saveNoteCancelable] (or any
  /// other cancelable gateway call), matching
  /// core/mobileapi.Service.Cancel. A no-op if requestId is unknown or
  /// already finished.
  Future<void> cancelRequest(String requestId);
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

  /// Returns the shared, platform-neutral synchronization summary (state,
  /// pending_count, last_success_unix_ms, retry_in_ms, unsafe_count,
  /// action_required) matching core/presentation.SyncSummary one-for-one -
  /// see core/mobileapi.Service.SyncSummary and desktop's identical
  /// SyncSummaryDTO.
  Future<Map<String, dynamic>> syncSummary();

  /// Lists the active workspace's quarantined incoming operations: a
  /// sanitized operation id, sequence, rejection reason, and receipt time -
  /// never ciphertext or other protocol detail - matching desktop's
  /// identical SyncQuarantineDTO.
  Future<List<Map<String, dynamic>>> listSyncQuarantine();

  /// Discards operationId's locally-rejected copy so the next cycle
  /// re-pulls and re-verifies it from scratch, reattaching the sync worker
  /// first if a prior quarantine detached it - see
  /// core/mobileapi.Service.RetryQuarantined.
  Future<void> retryQuarantined(String operationId);
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

  /// Returns the bounded facts the user diagnostics screen always shows,
  /// matching desktop's identical DiagnosticSummaryDTO - see
  /// core/mobileapi.Service.DiagnosticSummary. appVersion is supplied by
  /// the Flutter host, since the Go core has no knowledge of the Android
  /// app package's own version.
  Future<Map<String, dynamic>> diagnosticSummary(String appVersion);

  /// Returns the additional facts the "expand technical details" layer of
  /// the user diagnostics screen MAY show, matching desktop's identical
  /// TechnicalDiagnosticsDTO - see core/mobileapi.Service.TechnicalDiagnostics.
  Future<Map<String, dynamic>> technicalDiagnostics();

  /// Renders the current diagnostics as the sanitized plain-text bundle
  /// the "Copy diagnostics" action places on the clipboard - see
  /// core/mobileapi.Service.CopyDiagnostics.
  Future<String> copyDiagnostics(String appVersion);
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
  Future<void> saveNoteCancelable(
    String requestId,
    String id,
    String title,
    String body,
  ) => _channel.invokeMethod<dynamic>("saveNote", {
    "requestId": requestId,
    "noteId": id,
    "title": title,
    "body": body,
  });

  @override
  Future<void> cancelRequest(String requestId) =>
      _channel.invokeMethod<dynamic>("cancel", {"requestId": requestId});

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
  Future<Map<String, dynamic>> syncSummary() async =>
      _object(await _invoke("syncSummary"));

  @override
  Future<List<Map<String, dynamic>>> listSyncQuarantine() async =>
      _list(await _invoke("listSyncQuarantine"));

  @override
  Future<void> retryQuarantined(String operationId) =>
      _invoke("retryQuarantined", {"operationId": operationId});

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

  @override
  Future<Map<String, dynamic>> diagnosticSummary(String appVersion) async =>
      _object(
        await _invoke("diagnosticSummary", {"appVersion": appVersion}),
      );

  @override
  Future<Map<String, dynamic>> technicalDiagnostics() async =>
      _object(await _invoke("technicalDiagnostics"));

  @override
  Future<String> copyDiagnostics(String appVersion) async =>
      await _invoke("copyDiagnostics", {"appVersion": appVersion})
          as String;
}

import {
  AddAttachmentFromBytes,
  AddAttachmentFromFile,
  AutostartStatus,
  Catalog,
  CommitNoteBody,
  CreateAccount,
  CreateManualBackup,
  CreateNote,
  CreateSavedSearch,
  DefaultDatabasePath,
  DeleteSavedSearch,
  DiffRevisions,
  EnsureDailyBackup,
  ExportNotes,
  GetNoteDocument,
  GetSettings,
  ImportBerestaArchive,
  ImportEvernoteArchive,
  ListBackups,
  ListNoteAttachments,
  ListNotebooks,
  ListNotes,
  ListRevisions,
  ListSavedSearches,
  ListTags,
  LockAccount,
  PickAttachmentFile,
  PickBackupDirectory,
  PickExportDestination,
  PickImportSource,
  PickSaveDestination,
  PlanRestore,
  PreviewBackup,
  ReadAttachmentPreview,
  RemoveAttachment,
  RestoreRevision,
  RestoreSelective,
  RestoreWhole,
  RevisionMarkdown,
  SaveAttachmentToFile,
  Search,
  SearchByTag,
  Status,
  UnlockAccount,
  UpdateSavedSearch,
  UpdateSettings,
  VerifyAllBackups,
  VerifyBackup,
  WipeLocalAccount,
} from "../wailsjs/go/main/App";
import { main } from "../wailsjs/go/models";

/**
 * ApiError is the shape every failed Beresta bound call rejects with. Code
 * is the stable, switchable identifier (see desktop/errors.go's AppError);
 * Message is a diagnostic fallback only, never localized on its own -
 * screens must look up a localized string by Code (see i18n.tsx's
 * errorMessage).
 */
export interface ApiError {
  code: string;
  message: string;
}

/**
 * unwrapError normalizes whatever a rejected Wails binding call throws
 * into an ApiError. Beresta's own bound methods reject with an Error whose
 * `message` is a JSON-encoded {code, message} object (desktop/errors.go's
 * AppError.Error() docs explain why: Wails only ever transmits the plain
 * error string across the JS bridge). A handful of failures never reach
 * Go at all (a malformed call, a Wails runtime-level rejection) and stay
 * plain strings; those become code "internal" rather than throwing an
 * unhandled parse error.
 */
export function unwrapError(thrown: unknown): ApiError {
  const raw =
    thrown instanceof Error
      ? thrown.message
      : typeof thrown === "string"
        ? thrown
        : String(thrown);
  try {
    const parsed: unknown = JSON.parse(raw);
    if (
      parsed !== null &&
      typeof parsed === "object" &&
      "code" in parsed &&
      "message" in parsed &&
      typeof (parsed as { code: unknown }).code === "string" &&
      typeof (parsed as { message: unknown }).message === "string"
    ) {
      return parsed as ApiError;
    }
  } catch {
    // Not JSON: fall through to the generic case below.
  }
  return { code: "internal", message: raw };
}

export async function accountStatus(): Promise<main.AccountStatus> {
  return Status();
}

export async function getSettings(): Promise<main.AppSettings> {
  return GetSettings();
}

export async function updateSettings(
  next: main.AppSettings,
): Promise<main.AppSettings> {
  return UpdateSettings(next);
}

export async function localeCatalog(locale: string): Promise<main.LocaleCatalog> {
  return Catalog(locale);
}

/**
 * autostartStatus reports the live Windows Run-key state for this
 * install, which can drift from AppSettings.autostart_enabled if the
 * user removed the entry directly or a different install path left a
 * stale one behind (see desktop/shell.go's App.AutostartStatus).
 */
export async function autostartStatus(): Promise<main.AutostartStatusDTO> {
  return AutostartStatus();
}

export async function defaultDatabasePath(): Promise<string> {
  return DefaultDatabasePath();
}

export async function createAccount(
  req: main.CreateAccountRequest,
): Promise<main.AccountInfo> {
  return CreateAccount(req);
}

export async function unlockAccount(
  req: main.UnlockAccountRequest,
): Promise<main.AccountInfo> {
  return UnlockAccount(req);
}

export async function lockAccount(): Promise<void> {
  return LockAccount();
}

/**
 * pickDatabaseDestination opens the native save-file dialog for choosing
 * (or creating) a local account database location. It returns "" if the
 * user canceled, matching PickSaveDestination's Go-side contract.
 */
export async function pickDatabaseDestination(
  defaultFileName: string,
): Promise<string> {
  return PickSaveDestination(defaultFileName);
}

export async function listNotebooks(): Promise<main.NotebookDTO[]> {
  return ListNotebooks();
}

export async function listTags(): Promise<main.TagDTO[]> {
  return ListTags();
}

export async function listNotes(): Promise<main.NoteDTO[]> {
  return ListNotes();
}

export async function searchByTag(tagId: string): Promise<main.SearchResultDTO[]> {
  return SearchByTag(tagId);
}

/**
 * search runs the search-box filter language (bare words, `tag:`, `after:`,
 * `before:`, `deleted:true`) against the account's workspace. See
 * desktop/search.go's App.Search doc comment for the exact grammar.
 */
export async function search(text: string): Promise<main.SearchResultDTO[]> {
  return Search(text);
}

export async function listSavedSearches(): Promise<main.SavedSearchDTO[]> {
  return ListSavedSearches();
}

export async function createSavedSearch(
  name: string,
  query: string,
): Promise<main.SavedSearchDTO> {
  return CreateSavedSearch(name, query);
}

export async function updateSavedSearch(
  savedSearchId: string,
  name: string,
  query: string,
): Promise<void> {
  return UpdateSavedSearch(savedSearchId, name, query);
}

export async function deleteSavedSearch(savedSearchId: string): Promise<void> {
  return DeleteSavedSearch(savedSearchId);
}

export async function createNote(notebookId: string, title: string): Promise<main.NoteDTO> {
  return CreateNote(notebookId, title);
}

export async function getNoteDocument(noteId: string): Promise<main.NoteDocumentDTO> {
  return GetNoteDocument(noteId);
}

export async function commitNoteBody(req: main.CommitNoteBodyRequest): Promise<void> {
  return CommitNoteBody(req);
}

/**
 * pickAttachmentFile opens the native file-open dialog for choosing a file
 * to attach. It returns "" if the user canceled.
 */
export async function pickAttachmentFile(): Promise<string> {
  return PickAttachmentFile();
}

export async function listNoteAttachments(noteId: string): Promise<main.AttachmentDTO[]> {
  return ListNoteAttachments(noteId);
}

export async function addAttachmentFromFile(
  noteId: string,
  sourcePath: string,
): Promise<main.AttachmentDTO> {
  return AddAttachmentFromFile(noteId, sourcePath);
}

export async function addAttachmentFromBytes(
  noteId: string,
  displayName: string,
  mediaType: string,
  dataBase64: string,
): Promise<main.AttachmentDTO> {
  return AddAttachmentFromBytes(noteId, displayName, mediaType, dataBase64);
}

export async function removeAttachment(noteId: string, blobId: string): Promise<void> {
  return RemoveAttachment(noteId, blobId);
}

export async function readAttachmentPreview(blobId: string): Promise<main.AttachmentPreviewDTO> {
  return ReadAttachmentPreview(blobId);
}

/**
 * pickAttachmentSaveDestination opens the native save-file dialog,
 * pre-filled with defaultFileName, and returns the chosen path, or "" if
 * the user canceled.
 */
export async function pickAttachmentSaveDestination(defaultFileName: string): Promise<string> {
  return PickSaveDestination(defaultFileName);
}

export async function saveAttachmentToFile(
  blobId: string,
  destPath: string,
): Promise<main.AttachmentSaveResult> {
  return SaveAttachmentToFile(blobId, destPath);
}

export async function listRevisions(noteId: string): Promise<main.RevisionDTO[]> {
  return ListRevisions(noteId);
}

export async function revisionMarkdown(noteId: string, revisionId: string): Promise<string> {
  return RevisionMarkdown(noteId, revisionId);
}

/** diffRevisions diffs fromRevisionId to toRevisionId's content; an empty
 * fromRevisionId diffs against the note's state before its first
 * revision (see desktop/revisions.go's App.DiffRevisions). */
export async function diffRevisions(
  noteId: string,
  fromRevisionId: string,
  toRevisionId: string,
): Promise<main.DiffLineDTO[]> {
  return DiffRevisions(noteId, fromRevisionId, toRevisionId);
}

/** restoreRevision creates a new current revision matching a historical
 * one's plain-text content, without erasing the intervening history. */
export async function restoreRevision(noteId: string, revisionId: string): Promise<void> {
  return RestoreRevision(noteId, revisionId);
}

/**
 * pickBackupDirectory opens the native directory picker for the external
 * destination daily/manual/restore-safety backups write to. It returns ""
 * if the user canceled.
 */
export async function pickBackupDirectory(): Promise<string> {
  return PickBackupDirectory();
}

export async function listBackups(kind: string): Promise<main.BackupDTO[]> {
  return ListBackups(kind);
}

export async function createManualBackup(destRoot: string): Promise<main.BackupDTO> {
  return CreateManualBackup(destRoot);
}

/** ensureDailyBackup creates today's daily backup under destRoot if one
 * does not already exist yet, rotating old daily backups down to the
 * retained seven. Returns whether a new backup was actually created. */
export async function ensureDailyBackup(destRoot: string): Promise<boolean> {
  return EnsureDailyBackup(destRoot);
}

export async function verifyAllBackups(): Promise<void> {
  return VerifyAllBackups();
}

export async function verifyBackup(backupId: string): Promise<void> {
  return VerifyBackup(backupId);
}

export async function previewBackup(backupId: string): Promise<main.BackupPreviewDTO> {
  return PreviewBackup(backupId);
}

/** planRestore computes, without mutating current data, what restoring
 * noteIds (or the whole backup, when noteIds is empty) from backupId
 * would do. */
export async function planRestore(backupId: string, noteIds: string[]): Promise<main.RestorePlanDTO> {
  return PlanRestore(backupId, noteIds);
}

export async function restoreSelective(
  backupId: string,
  noteIds: string[],
  destRoot: string,
): Promise<main.RestoreResultDTO> {
  return RestoreSelective(backupId, noteIds, destRoot);
}

export async function restoreWhole(backupId: string, destRoot: string): Promise<main.RestoreResultDTO> {
  return RestoreWhole(backupId, destRoot);
}

/**
 * pickExportDestination opens the native directory picker for a new
 * export destination. ExportNotes requires a directory that does not
 * already exist, so callers should offer the picked path plus a
 * caller-supplied subfolder name rather than the picked directory itself.
 */
export async function pickExportDestination(): Promise<string> {
  return PickExportDestination();
}

/**
 * pickImportSource opens the native directory picker (kind "beresta") or
 * file picker (kind "evernote") for an import source. Returns "" if the
 * user canceled.
 */
export async function pickImportSource(kind: "beresta" | "evernote"): Promise<string> {
  return PickImportSource(kind);
}

/** exportNotes writes noteIds (or every note, when noteIds is empty) as
 * plaintext Markdown/attachments/manifest.json to destDir, which must not
 * already exist. This is the confirmed export action - callers are
 * responsible for the required warning/confirmation before calling it. */
export async function exportNotes(
  destDir: string,
  noteIds: string[],
): Promise<main.ExportManifestDTO> {
  return ExportNotes(destDir, noteIds);
}

export async function importBerestaArchive(sourceDir: string): Promise<main.ImportResultDTO> {
  return ImportBerestaArchive(sourceDir);
}

export async function importEvernoteArchive(path: string): Promise<main.ImportResultDTO> {
  return ImportEvernoteArchive(path);
}

/**
 * wipeLocalAccount permanently deletes every local file this device holds
 * for the account at databasePath - the database, its key envelope, and
 * the attachment blob store - without requiring it to be unlocked first.
 * Callers are responsible for an explicit irreversible-confirmation step
 * before calling this; it performs no confirmation of its own.
 */
export async function wipeLocalAccount(databasePath: string): Promise<void> {
  return WipeLocalAccount(databasePath);
}

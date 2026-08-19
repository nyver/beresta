import {
  Catalog,
  CreateAccount,
  DefaultDatabasePath,
  GetSettings,
  ListNotebooks,
  ListNotes,
  ListTags,
  LockAccount,
  PickSaveDestination,
  SearchByTag,
  Status,
  UnlockAccount,
  UpdateSettings,
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

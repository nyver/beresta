import { appMock } from "./setupTests";
import { main } from "../wailsjs/go/models";

/**
 * identityCatalog returns the requested key itself for any lookup, so
 * component tests can assert on stable key names (e.g.
 * "onboarding.create_button") instead of duplicating translated copy from
 * locales/en.json, which would drift out of sync silently.
 */
export const identityCatalog: Record<string, string> = new Proxy(
  {},
  { get: (_target, prop: string) => prop },
);

export function mockLocaleCatalog(locale: "en" | "ru" = "en") {
  appMock.Catalog.mockResolvedValue({ locale, strings: identityCatalog, supported: ["en", "ru"] });
}

/**
 * mockSavedSearches primes ListSavedSearches with a resolved default.
 * SearchBar (mounted unconditionally inside Shell) fetches saved searches
 * on mount, so any Shell-rendering test needs this configured before
 * render or the unconfigured mock's `undefined` return breaks that effect.
 */
export function mockSavedSearches(searches: main.SavedSearchDTO[] = []) {
  appMock.ListSavedSearches.mockResolvedValue(searches);
}

export function mockSettings(overrides: Partial<main.AppSettings> = {}) {
  const settings: main.AppSettings = {
    language: "en",
    last_database_path: "",
    auto_lock_minutes: 15,
    backup_directory: "C:\\Users\\test\\Beresta\\backups",
    quick_note_hotkey: "Ctrl+Shift+N",
    autostart_enabled: false,
    sync_enabled: false,
    sync_server_url: "",
    sync_security_mode: "pinned",
    sync_fingerprint: "",
    active_workspace_id: "",
    ...overrides,
  };
  appMock.GetSettings.mockResolvedValue(settings);
  appMock.UpdateSettings.mockResolvedValue(settings);
  return settings;
}

export function mockSyncStatus(status = "disabled") {
  appMock.SyncStatus.mockResolvedValue(status);
  appMock.ListSyncDevices.mockResolvedValue([]);
  appMock.ListSyncQuarantine.mockResolvedValue([]);
  appMock.ExportIdentity.mockResolvedValue("beresta://identity?user=test&key=00");
  appMock.ListWorkspaces.mockResolvedValue([]);
}

export function mockAutostartStatus(overrides: Partial<main.AutostartStatusDTO> = {}) {
  const status: main.AutostartStatusDTO = { enabled: false, conflict_path: "", ...overrides };
  appMock.AutostartStatus.mockResolvedValue(status);
  return status;
}

export function mockLockedStatus() {
  appMock.Status.mockResolvedValue({ unlocked: false });
}

export function mockUnlockedStatus(account: main.AccountInfo) {
  appMock.Status.mockResolvedValue({ unlocked: true, account });
}

export function fakeAccountInfo(overrides: Partial<main.AccountInfo> = {}): main.AccountInfo {
  return {
    account_id: "0000-account",
    device_id: "0000-device",
    workspace_id: "0000-workspace",
    key_protection: "WindowsDPAPI",
    ...overrides,
  };
}

let fixtureCounter = 0;
function nextFixtureId(prefix: string): string {
  fixtureCounter += 1;
  return `${prefix}-${fixtureCounter}`;
}

export function fakeNotebook(overrides: Partial<main.NotebookDTO> = {}): main.NotebookDTO {
  return {
    id: nextFixtureId("notebook"),
    workspace_id: "workspace",
    parent_id: "",
    name: "Notebook",
    deleted: false,
    ...overrides,
  };
}

export function fakeTag(overrides: Partial<main.TagDTO> = {}): main.TagDTO {
  return {
    id: nextFixtureId("tag"),
    workspace_id: "workspace",
    name: "Tag",
    deleted: false,
    ...overrides,
  };
}

export function fakeNote(overrides: Partial<main.NoteDTO> = {}): main.NoteDTO {
  return {
    id: nextFixtureId("note"),
    workspace_id: "workspace",
    notebook_id: "",
    title: "Note",
    pinned: false,
    archived: false,
    deleted: false,
    created_unix_ms: Date.UTC(2026, 0, 1),
    updated_unix_ms: Date.UTC(2026, 0, 1),
    preview: "",
    ...overrides,
  };
}

export function fakeSavedSearch(overrides: Partial<main.SavedSearchDTO> = {}): main.SavedSearchDTO {
  return {
    id: nextFixtureId("saved-search"),
    workspace_id: "workspace",
    name: "Saved search",
    query: "project",
    created_unix_ms: Date.UTC(2026, 0, 1),
    updated_unix_ms: Date.UTC(2026, 0, 1),
    ...overrides,
  };
}

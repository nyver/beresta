import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";

/**
 * appMock stubs the generated Wails bindings (window.go.main.App.*) that
 * every screen calls through src/api.ts. Wails only installs window.go at
 * runtime inside the real desktop shell, so component tests need this
 * stand-in; tests configure individual methods per case with
 * mockResolvedValueOnce/mockRejectedValueOnce.
 */
export const appMock = {
  Status: vi.fn(),
  GetSettings: vi.fn(),
  UpdateSettings: vi.fn(),
  Catalog: vi.fn(),
  DefaultDatabasePath: vi.fn(),
  CreateAccount: vi.fn(),
  UnlockAccount: vi.fn(),
  LockAccount: vi.fn(),
  SyncStatus: vi.fn(),
  ConnectServer: vi.fn(),
  DiagnoseServer: vi.fn(),
  DisableServer: vi.fn(),
  ListSyncDevices: vi.fn(),
  ListSyncQuarantine: vi.fn(),
  RetrySyncQuarantine: vi.fn(),
  RevokeSyncDevice: vi.fn(),
  ExportIdentity: vi.fn(),
  ShareWorkspace: vi.fn(),
  AcceptWorkspaceGrant: vi.fn(),
  ListWorkspaceMembers: vi.fn(),
  ListWorkspaces: vi.fn(),
  RevokeWorkspaceMember: vi.fn(),
  SetActiveWorkspace: vi.fn(),
  PickSaveDestination: vi.fn(),
  ListNotebooks: vi.fn(),
  CreateNotebook: vi.fn(),
  SetNotebookDeleted: vi.fn(),
  MoveNotebook: vi.fn(),
  SetNoteNotebook: vi.fn(),
  ListTags: vi.fn(),
  CreateTag: vi.fn(),
  SetTagDeleted: vi.fn(),
  SetNoteTag: vi.fn(),
  NoteTagsByWorkspace: vi.fn(),
  ListNotes: vi.fn(),
  SearchByTag: vi.fn(),
  Search: vi.fn(),
  ListSavedSearches: vi.fn(),
  CreateSavedSearch: vi.fn(),
  UpdateSavedSearch: vi.fn(),
  DeleteSavedSearch: vi.fn(),
  GetNoteDocument: vi.fn(),
  CommitNoteBody: vi.fn(),
  CreateNote: vi.fn(),
  DeleteNote: vi.fn(),
  PickAttachmentFile: vi.fn(),
  ListNoteAttachments: vi.fn(),
  AddAttachmentFromFile: vi.fn(),
  AddAttachmentFromBytes: vi.fn(),
  RemoveAttachment: vi.fn(),
  ReadAttachmentPreview: vi.fn(),
  SaveAttachmentToFile: vi.fn(),
  ListRevisions: vi.fn(),
  RevisionMarkdown: vi.fn(),
  DiffRevisions: vi.fn(),
  RestoreRevision: vi.fn(),
  PickBackupDirectory: vi.fn(),
  ListBackups: vi.fn(),
  CreateManualBackup: vi.fn(),
  EnsureDailyBackup: vi.fn(),
  VerifyAllBackups: vi.fn(),
  VerifyBackup: vi.fn(),
  PreviewBackup: vi.fn(),
  PlanRestore: vi.fn(),
  RestoreSelective: vi.fn(),
  RestoreWhole: vi.fn(),
  PickExportDestination: vi.fn(),
  PickImportSource: vi.fn(),
  ExportNotes: vi.fn(),
  ImportBerestaArchive: vi.fn(),
  ImportEvernoteArchive: vi.fn(),
  WipeLocalAccount: vi.fn(),
  AutostartStatus: vi.fn(),
};

(globalThis as unknown as { go: { main: { App: typeof appMock } } }).go = {
  main: { App: appMock },
};

/**
 * runtimeMock stubs window.runtime, the Wails runtime bridge that
 * AttachmentPanel's native drag-and-drop uses directly (OnFileDrop /
 * OnFileDropOff, see desktop/frontend/wailsjs/runtime/runtime.js) rather
 * than through window.go.main.App like every other bound call.
 */
export const runtimeMock = {
  OnFileDrop: vi.fn(),
  OnFileDropOff: vi.fn(),
  // Backs wailsjs/runtime's EventsOn/EventsOff (see Shell.tsx's
  // quick-note capture listener, which - like AttachmentPanel's
  // OnFileDrop/OnFileDropOff above - unsubscribes through EventsOff
  // rather than through EventsOn's return value, since beforeEach's
  // mockReset() below would otherwise clear any canned return value
  // between tests.
  EventsOnMultiple: vi.fn(),
  EventsOff: vi.fn(),
  EventsOffAll: vi.fn(),
};

(globalThis as unknown as { runtime: typeof runtimeMock }).runtime = runtimeMock;

// jsdom has no layout engine, so every element reports a 0x0 box and has
// no ResizeObserver at all. @tanstack/react-virtual (the NoteList's
// virtualization, see shell/NoteList.tsx) measures its scroll container
// through @tanstack/virtual-core's getRect(), which reads
// offsetWidth/offsetHeight specifically (not getBoundingClientRect and
// not clientHeight/clientWidth) - without stubbing those two, the
// virtualizer always computes a 0-height viewport and renders zero rows.
// ResizeObserver itself is stubbed separately only so the library's
// unconditional `new ResizeObserver(...)` call does not throw; it never
// needs to actually fire for a static test scenario like this one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
(globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver =
  ResizeObserverStub;
Object.defineProperty(HTMLElement.prototype, "offsetWidth", { configurable: true, value: 800 });
Object.defineProperty(HTMLElement.prototype, "offsetHeight", { configurable: true, value: 600 });

beforeEach(() => {
  for (const fn of Object.values(appMock)) {
    fn.mockReset();
  }
  for (const fn of Object.values(runtimeMock)) {
    fn.mockReset();
  }
  // Shell.tsx's loadAll always fetches this alongside notebooks/tags/notes;
  // most tests have no need to exercise per-note tag assignment, so this
  // default (individually overridable via mockResolvedValue, same as any
  // other appMock entry) avoids repeating it in every test that renders
  // Shell.
  appMock.NoteTagsByWorkspace.mockResolvedValue({});
});

// @testing-library/react's own automatic-cleanup registration only fires
// when it detects a global `afterEach` (i.e. Vitest's `test.globals: true`
// config), which this project deliberately does not enable so every test
// file keeps its explicit `import { ... } from "vitest"` style. Without
// this, unmounted trees from a previous test stay in the jsdom document
// and later tests' role/label queries can match stale duplicates.
afterEach(() => {
  cleanup();
});

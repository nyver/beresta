import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import * as Y from "yjs";

import { bytesToBase64 } from "../editor/base64";
import { I18nProvider } from "../i18n";
import { appMock, runtimeMock } from "../setupTests";
import {
  fakeAccountInfo,
  fakeNote,
  fakeNotebook,
  fakeTag,
  mockLocaleCatalog,
  mockSavedSearches,
  mockSettings,
  mockSyncStatus,
} from "../testUtils";
import { Shell } from "./Shell";
import { main } from "../../wailsjs/go/models";

function mockEmptyNoteDocument() {
  const doc = new Y.Doc();
  const update = Y.encodeStateAsUpdate(doc);
  doc.destroy();
  appMock.GetNoteDocument.mockResolvedValue({ update_base64: bytesToBase64(update), format: "v1" });
  appMock.CommitNoteBody.mockResolvedValue(undefined);
  appMock.ListNoteAttachments.mockResolvedValue([]);
  appMock.ListRevisions.mockResolvedValue([]);
}

function renderShell(
  options: { settings?: Partial<main.AppSettings>; account?: main.AccountInfo } = {},
) {
  mockLocaleCatalog();
  mockSettings(options.settings);
  mockSavedSearches();
  const onLocked = vi.fn();
  render(
    <I18nProvider>
      <Shell account={options.account ?? fakeAccountInfo()} onLocked={onLocked} />
    </I18nProvider>,
  );
  return { onLocked };
}

describe("Shell", () => {
  it("loads and shows every note under All Notes by default", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([fakeNote({ title: "Grocery list" })]);
    renderShell();

    expect(await screen.findByText("Grocery list")).toBeInTheDocument();
    expect(screen.getByRole("main")).toHaveAccessibleName("shell.title");
  });

  it("orders notes by their most recent modification time", async () => {
    const older = fakeNote({ title: "Older note", updated_unix_ms: 100 });
    const newer = fakeNote({ title: "Newer note", updated_unix_ms: 200 });
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([older, newer]);
    renderShell();

    const list = await screen.findByRole("listbox");
    expect(within(list).getAllByRole("option").map((option) => option.textContent)).toEqual([
      expect.stringContaining("Newer note"),
      expect.stringContaining("Older note"),
    ]);
  });

  it("excludes tombstoned notes from every view", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([
      fakeNote({ title: "Live note" }),
      fakeNote({ title: "Deleted note", deleted: true }),
    ]);
    renderShell();

    expect(await screen.findByText("Live note")).toBeInTheDocument();
    expect(screen.queryByText("Deleted note")).not.toBeInTheDocument();
  });

  it("filters the note list to a selected notebook", async () => {
    const notebook = fakeNotebook({ name: "Work" });
    appMock.ListNotebooks.mockResolvedValue([notebook]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([
      fakeNote({ title: "In workspace root" }),
      fakeNote({ title: "In Work", notebook_id: notebook.id }),
    ]);
    renderShell();
    const user = userEvent.setup();

    await screen.findByText("In workspace root");
    await user.click(await screen.findByRole("button", { name: "Work" }));

    expect(await screen.findByText("In Work")).toBeInTheDocument();
    expect(screen.queryByText("In workspace root")).not.toBeInTheDocument();
  });

  it("filters the note list to a selected tag via SearchByTag", async () => {
    const tag = fakeTag({ name: "urgent" });
    const taggedNote = fakeNote({ title: "Tagged note" });
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([tag]);
    appMock.ListNotes.mockResolvedValue([fakeNote({ title: "Other note" }), taggedNote]);
    appMock.SearchByTag.mockResolvedValue([{ note: taggedNote, rank: 0 }]);
    renderShell();
    const user = userEvent.setup();

    await screen.findByText("Other note");
    await user.click(await screen.findByRole("button", { name: "urgent" }));

    expect(await screen.findByText("Tagged note")).toBeInTheDocument();
    expect(screen.queryByText("Other note")).not.toBeInTheDocument();
    expect(appMock.SearchByTag).toHaveBeenCalledWith(tag.id);
  });

  it("shows a renamed note's new title in the list while a tag filter is active", async () => {
    const tag = fakeTag({ name: "urgent" });
    const taggedNote = fakeNote({ title: "Tagged note" });
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([tag]);
    appMock.ListNotes.mockResolvedValue([taggedNote]);
    appMock.SearchByTag.mockResolvedValue([{ note: taggedNote, rank: 0 }]);
    mockEmptyNoteDocument();
    renderShell();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "urgent" }));
    await user.click(await screen.findByText("Tagged note"));

    const titleInput = await screen.findByLabelText("shell.detail_title_label");
    await user.clear(titleInput);
    await user.type(titleInput, "Renamed while tag-filtered");
    await user.tab();

    expect(await screen.findByText("Renamed while tag-filtered")).toBeInTheDocument();
    expect(screen.queryByText("Tagged note")).not.toBeInTheDocument();
  });

  it("overrides the note list with search results, then restores the sidebar selection when a notebook is clicked", async () => {
    const notebook = fakeNotebook({ name: "Work" });
    const browsedNote = fakeNote({ title: "Browsed note" });
    const foundNote = fakeNote({ title: "Found via search" });
    appMock.ListNotebooks.mockResolvedValue([notebook]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([browsedNote]);
    appMock.Search.mockResolvedValue([{ note: foundNote, rank: 0 }]);
    renderShell();
    const user = userEvent.setup();

    await screen.findByText("Browsed note");
    // A bare word would now be matched client-side against the already-
    // loaded note list (task 1.2's partial title match), which never
    // includes a search-only result like foundNote; a filter token routes
    // it through the mocked backend Search() call instead, exercising the
    // override-then-restore behavior this test is actually about.
    await user.type(screen.getByPlaceholderText("search.placeholder"), "deleted:true");

    // The typed query also becomes a highlight term, so "Found" renders
    // inside its own <mark>, splitting the row's text across elements -
    // findByText's exact string match cannot see across that split. The
    // listbox itself is re-queried on every poll (not captured once)
    // because the client-side title match briefly renders the "no
    // results" message in its place while "deleted:true" is still being
    // typed one character at a time, before the debounced backend call
    // (routed here by the filter token) settles - re-querying avoids
    // asserting against a node React has already detached.
    await waitFor(() =>
      expect(within(screen.getByRole("listbox")).getByRole("option")).toHaveTextContent("Found via search"),
    );
    expect(screen.queryByText("Browsed note")).not.toBeInTheDocument();

    await user.click(await screen.findByRole("button", { name: "Work" }));

    await waitFor(() => expect(screen.queryByRole("listbox")).not.toBeInTheDocument());
    expect(screen.getByPlaceholderText("search.placeholder")).toHaveValue("");
  });

  it("shows a retryable error when loading fails", async () => {
    appMock.ListNotebooks.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    renderShell();

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");

    appMock.ListNotebooks.mockResolvedValue([]);
    await userEvent.setup().click(screen.getByRole("button", { name: "common.retry" }));

    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });

  it("locks the account and reports it locked", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    appMock.LockAccount.mockResolvedValue(undefined);
    const { onLocked } = renderShell();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.lock_button" }));

    await waitFor(() => expect(onLocked).toHaveBeenCalled());
    expect(appMock.LockAccount).toHaveBeenCalled();
  });

  it("hides note content behind a locking overlay immediately, before LockAccount resolves", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([fakeNote({ title: "Secret note" })]);
    let resolveLock: () => void = () => {};
    appMock.LockAccount.mockReturnValue(new Promise<void>((resolve) => (resolveLock = resolve)));
    renderShell();
    const user = userEvent.setup();

    await screen.findByText("Secret note");
    await user.click(screen.getByRole("button", { name: "shell.lock_button" }));

    expect(await screen.findByText("shell.locking_message")).toBeInTheDocument();
    expect(screen.queryByText("Secret note")).not.toBeInTheDocument();

    resolveLock();
    await waitFor(() => expect(appMock.LockAccount).toHaveBeenCalled());
  });

  it("shows a key-protection badge reflecting the account's actual protection mode", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    renderShell({ account: fakeAccountInfo({ key_protection: "windows-hello" }) });

    expect(await screen.findByText("shell.key_protection_hello")).toBeInTheDocument();
  });

  it("opens synchronization settings with the current local device", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    mockSyncStatus();
    renderShell({ account: fakeAccountInfo({ device_id: "device-local" }) });

    await userEvent.setup().click(await screen.findByRole("button", { name: "sync.open_button" }));

    const dialog = await screen.findByRole("dialog", { name: "sync.title" });
    expect(within(dialog).getByText("device-local")).toBeInTheDocument();
    // The topbar's own compact status pill (task: passive sync status
    // instead of a plain button) now shows this same "sync.status_disabled"
    // text outside the dialog too, so this must be scoped to the dialog to
    // stay unambiguous.
    expect(within(dialog).getByText("sync.status_disabled")).toBeInTheDocument();
  });

  it("starts an immediate synchronization cycle for the active workspace", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    appMock.SyncNow.mockResolvedValue(undefined);
    mockSyncStatus("current");
    renderShell();

    const button = await screen.findByRole("button", { name: "sync.force_button" });
    await waitFor(() => expect(button).toBeEnabled());
    await userEvent.setup().click(button);

    expect(appMock.SyncNow).toHaveBeenCalledTimes(1);
    expect(await screen.findByRole("button", { name: "sync.force_button" })).toHaveAttribute(
      "aria-busy",
      "false",
    );
  });

  it("reloads note titles once when an incoming synchronization cycle completes", async () => {
    const note = fakeNote({ title: "Before mobile rename" });
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([note]);
    mockSyncStatus("active");
    renderShell();

    expect(await screen.findByText("Before mobile rename")).toBeInTheDocument();
    await waitFor(() => expect(runtimeMock.EventsOnMultiple).toHaveBeenCalled());
    const [, onSyncStatus] =
      runtimeMock.EventsOnMultiple.mock.calls.find(([eventName]) => eventName === "sync:status") ?? [];
    expect(onSyncStatus).toBeDefined();

    appMock.ListNotes.mockResolvedValue([{ ...note, title: "Renamed on mobile" }]);
    act(() => onSyncStatus?.("current"));

    expect(await screen.findByText("Renamed on mobile")).toBeInTheDocument();
    expect(appMock.ListNotes).toHaveBeenCalledTimes(2);

    act(() => onSyncStatus?.("current"));
    await waitFor(() => expect(appMock.ListNotes).toHaveBeenCalledTimes(2));
  });

  it("changes the auto-lock duration through the Settings modal and persists it", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    // The Settings modal now also mounts BackupsPanel (auto-lock moved in
    // alongside it), which needs its own ListBackups fetch mocked or it
    // renders with backups still undefined.
    appMock.ListBackups.mockResolvedValue([]);
    appMock.UpdateSettings.mockResolvedValue({
      language: "en",
      last_database_path: "",
      auto_lock_minutes: 30,
      backup_directory: "C:\\backups",
    });
    renderShell();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "settings.title" }));
    const select = await screen.findByLabelText("shell.auto_lock_label");
    // The control starts disabled until the initial GetSettings() fetch
    // resolves and arms autoLockMinutes; selecting an option before then
    // would silently no-op.
    await waitFor(() => expect(select).toBeEnabled());
    await user.selectOptions(select, "30");

    await waitFor(() =>
      expect(appMock.UpdateSettings).toHaveBeenCalledWith(expect.objectContaining({ auto_lock_minutes: 30 })),
    );
  });

  it("locks automatically after the configured idle timeout with no activity", async () => {
    vi.useFakeTimers();
    try {
      appMock.ListNotebooks.mockResolvedValue([]);
      appMock.ListTags.mockResolvedValue([]);
      appMock.ListNotes.mockResolvedValue([]);
      appMock.LockAccount.mockResolvedValue(undefined);
      renderShell({ settings: { auto_lock_minutes: 5 } });

      // Flush the initial settings fetch that arms the idle timer, then
      // advance past the full 5-minute window with no simulated activity.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
      });

      expect(appMock.LockAccount).toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not lock automatically when auto-lock is disabled", async () => {
    vi.useFakeTimers();
    try {
      appMock.ListNotebooks.mockResolvedValue([]);
      appMock.ListTags.mockResolvedValue([]);
      appMock.ListNotes.mockResolvedValue([]);
      appMock.LockAccount.mockResolvedValue(undefined);
      renderShell({ settings: { auto_lock_minutes: 0 } });

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(60 * 60 * 1000);
      });

      expect(appMock.LockAccount).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("creates a new note at the workspace root with Ctrl+N and opens it for editing", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    mockEmptyNoteDocument();
    appMock.CreateNote.mockResolvedValue(fakeNote({ id: "new-note", title: "" }));
    renderShell();
    const user = userEvent.setup();

    await user.keyboard("{Control>}n{/Control}");

    expect(appMock.CreateNote).toHaveBeenCalledWith("", "");
    expect(
      await screen.findByLabelText("shell.detail_title_label", {}, { timeout: 5000 }),
    ).toBeInTheDocument();
  });

  it("switches to another notebook and opens the note created from its menu", async () => {
    const currentNotebook = fakeNotebook({ name: "Current" });
    const destinationNotebook = fakeNotebook({ name: "Destination" });
    appMock.ListNotebooks.mockResolvedValue([currentNotebook, destinationNotebook]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    mockEmptyNoteDocument();
    appMock.CreateNote.mockResolvedValue(
      fakeNote({ id: "new-note", title: "", notebook_id: destinationNotebook.id }),
    );
    renderShell();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Current" }));
    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Destination" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.new_note_button" }));

    expect(appMock.CreateNote).toHaveBeenCalledWith(destinationNotebook.id, "");
    expect(await screen.findByRole("button", { name: "Destination" })).toHaveClass("selected");
    expect(
      await screen.findByLabelText("shell.detail_title_label", {}, { timeout: 5000 }),
    ).toBeInTheDocument();
  });

  it("deletes the selected notebook and falls back to All Notes", async () => {
    const notebook = fakeNotebook({ name: "Work" });
    appMock.ListNotebooks.mockResolvedValue([notebook]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    appMock.SetNotebookDeleted.mockResolvedValue(undefined);
    renderShell();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Work" }));
    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.delete_notebook" }));
    await user.click(await screen.findByRole("button", { name: "shell.delete_confirm_button" }));

    expect(appMock.SetNotebookDeleted).toHaveBeenCalledWith(notebook.id, true);
    await waitFor(() => expect(screen.queryByRole("button", { name: "Work" })).not.toBeInTheDocument());
    expect(await screen.findByRole("button", { name: "shell.all_notes" })).toHaveClass("selected");
  });

  it("deletes the open note and clears the editor selection", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    const note = fakeNote({ title: "Grocery list" });
    appMock.ListNotes.mockResolvedValue([note]);
    mockEmptyNoteDocument();
    appMock.DeleteNote.mockResolvedValue(undefined);
    renderShell();
    const user = userEvent.setup();

    await user.click(await screen.findByText("Grocery list"));
    await user.click(await screen.findByRole("button", { name: "shell.note_actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.delete_note" }));
    await user.click(await screen.findByRole("button", { name: "shell.delete_confirm_button" }));

    expect(appMock.DeleteNote).toHaveBeenCalledWith(note.id);
    expect(screen.queryByText("shell.detail_placeholder")).not.toBeInTheDocument();
    expect(screen.queryByText("Grocery list")).not.toBeInTheDocument();
  });

  it("opens the quick-note capture panel on quicknote:open and reloads notes once it closes", async () => {
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([]);
    appMock.ListNotes.mockResolvedValue([]);
    mockEmptyNoteDocument();
    appMock.CreateNote.mockResolvedValue(fakeNote({ title: "" }));
    renderShell();

    // ready (and thus the quicknote:open subscription effect) only
    // becomes true once the mocked locale catalog resolves, which is not
    // yet true on Shell's very first synchronous render.
    await waitFor(() => expect(runtimeMock.EventsOnMultiple).toHaveBeenCalled());
    const [, onQuickNoteOpen] =
      runtimeMock.EventsOnMultiple.mock.calls.find(([eventName]) => eventName === "quicknote:open") ?? [];
    expect(onQuickNoteOpen).toBeDefined();

    act(() => onQuickNoteOpen?.());

    expect(await screen.findByRole("dialog", { name: "quicknote.title" })).toBeInTheDocument();
    expect(appMock.CreateNote).toHaveBeenCalledWith("", "");

    const listNotesCallsBeforeClose = appMock.ListNotes.mock.calls.length;
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "quicknote.done_button" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog", { name: "quicknote.title" })).not.toBeInTheDocument();
    });
    expect(appMock.ListNotes.mock.calls.length).toBeGreaterThan(listNotesCallsBeforeClose);
  });
});

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import * as Y from "yjs";

import { bytesToBase64 } from "../editor/base64";
import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { fakeNote, mockLocaleCatalog, mockSettings } from "../testUtils";
import { NoteEditorPane, type NoteEditorPaneHandle, type NoteEditorPaneProps } from "./NoteEditorPane";

function mockEmptyDocument() {
  const doc = new Y.Doc();
  const update = Y.encodeStateAsUpdate(doc);
  doc.destroy();
  appMock.GetNoteDocument.mockResolvedValue({ update_base64: bytesToBase64(update), format: "v1" });
  appMock.CommitNoteBody.mockResolvedValue(undefined);
  appMock.ListNoteAttachments.mockResolvedValue([]);
  appMock.ListRevisions.mockResolvedValue([]);
}

/** Every prop NoteEditorPane now requires, with harmless no-op defaults so
 * each test only overrides what it actually exercises. */
function baseProps(overrides: Partial<NoteEditorPaneProps> = {}): NoteEditorPaneProps {
  return {
    note: null,
    tags: [],
    assignedTagIds: [],
    onTitleCommitted: vi.fn(),
    onDeleted: vi.fn(),
    onCreateNote: vi.fn(),
    creatingNote: false,
    onToggleTag: vi.fn(),
    onCreateTag: vi.fn(),
    ...overrides,
  };
}

describe("NoteEditorPane", () => {
  it("shows the placeholder when no note is selected", async () => {
    mockLocaleCatalog();
    mockSettings();
    render(
      <I18nProvider>
        <NoteEditorPane {...baseProps()} />
      </I18nProvider>,
    );
    expect(await screen.findByText("shell.detail_placeholder")).toBeInTheDocument();
  });

  it("creates a note from the placeholder", async () => {
    mockLocaleCatalog();
    mockSettings();
    const onCreateNote = vi.fn();
    render(
      <I18nProvider>
        <NoteEditorPane {...baseProps({ onCreateNote })} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.new_note_button" }));
    expect(onCreateNote).toHaveBeenCalled();
  });

  it("renames the note on blur and reports the committed title", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockEmptyDocument();
    const note = fakeNote({ title: "Old title" });
    const onTitleCommitted = vi.fn();
    render(
      <I18nProvider>
        <NoteEditorPane {...baseProps({ note, onTitleCommitted })} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    const titleInput = await screen.findByLabelText("shell.detail_title_label");
    await user.clear(titleInput);
    await user.type(titleInput, "New title");
    await user.tab();

    await waitFor(() => expect(onTitleCommitted).toHaveBeenCalledWith(note.id, "New title"));
    expect(appMock.CommitNoteBody).toHaveBeenCalledWith(
      expect.objectContaining({ note_id: note.id, title: "New title" }),
    );
  });

  it("does not commit on blur when the title is unchanged", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockEmptyDocument();
    const note = fakeNote({ title: "Same title" });
    render(
      <I18nProvider>
        <NoteEditorPane {...baseProps({ note })} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    const titleInput = await screen.findByLabelText("shell.detail_title_label");
    await user.click(titleInput);
    await user.tab();

    expect(appMock.CommitNoteBody).not.toHaveBeenCalled();
  });

  it("does not report the title committed when the commit fails, and shows an error", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockEmptyDocument();
    appMock.CommitNoteBody.mockReset();
    appMock.CommitNoteBody.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "disk full" })),
    );
    const note = fakeNote({ title: "Old title" });
    const onTitleCommitted = vi.fn();
    render(
      <I18nProvider>
        <NoteEditorPane {...baseProps({ note, onTitleCommitted })} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    const titleInput = await screen.findByLabelText("shell.detail_title_label");
    await user.clear(titleInput);
    await user.type(titleInput, "New title");
    await user.tab();

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
    expect(onTitleCommitted).not.toHaveBeenCalled();
  });

  it("exposes an imperative flush that commits an in-progress rename", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockEmptyDocument();
    const note = fakeNote({ title: "Old title" });
    const onTitleCommitted = vi.fn();
    const ref = createRef<NoteEditorPaneHandle>();
    render(
      <I18nProvider>
        <NoteEditorPane ref={ref} {...baseProps({ note, onTitleCommitted })} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    const titleInput = await screen.findByLabelText("shell.detail_title_label");
    await user.clear(titleInput);
    await user.type(titleInput, "Renamed without blur");

    await ref.current!.flush();

    expect(onTitleCommitted).toHaveBeenCalledWith(note.id, "Renamed without blur");
  });

  it("flushes a pending body edit before restoring a revision, so it cannot reappear after the restore", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockEmptyDocument();
    appMock.ListRevisions.mockResolvedValue([
      { id: "rev-1", checkpoint: false, created_unix_ms: Date.UTC(2026, 0, 1) },
    ]);
    appMock.DiffRevisions.mockResolvedValue([]);
    const callOrder: string[] = [];
    appMock.CommitNoteBody.mockImplementation(async () => {
      callOrder.push("commit");
    });
    appMock.RestoreRevision.mockImplementation(async () => {
      callOrder.push("restore");
    });
    const note = fakeNote({ title: "Title" });
    render(
      <I18nProvider>
        <NoteEditorPane {...baseProps({ note })} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    const editor = await waitFor(() => {
      const element = document.querySelector(".ql-editor");
      if (!element) throw new Error("editor not mounted yet");
      return element as HTMLElement;
    });
    await user.click(editor);
    await user.type(editor, "pending edit");
    // Still under useNoteDocument's 800ms commit debounce: nothing sent yet.
    expect(appMock.CommitNoteBody).not.toHaveBeenCalled();

    await user.click(await screen.findByRole("button", { name: "shell.note_actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "revisions.open_button" }));
    await user.click(await screen.findByRole("button", { name: /2026/ }));
    await user.click(await screen.findByRole("button", { name: "revisions.restore_button" }));
    await user.click(screen.getByRole("button", { name: "revisions.restore_confirm_button" }));

    await waitFor(() => expect(appMock.RestoreRevision).toHaveBeenCalled());
    expect(appMock.CommitNoteBody).toHaveBeenCalled();
    expect(callOrder).toEqual(["commit", "restore"]);
  });

  it("deletes the open note after the inline confirmation", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockEmptyDocument();
    appMock.DeleteNote.mockResolvedValue(undefined);
    const note = fakeNote({ title: "Title" });
    const onDeleted = vi.fn();
    render(
      <I18nProvider>
        <NoteEditorPane {...baseProps({ note, onDeleted })} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.note_actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.delete_note" }));
    await user.click(await screen.findByRole("button", { name: "shell.delete_confirm_button" }));

    expect(appMock.DeleteNote).toHaveBeenCalledWith(note.id);
    expect(onDeleted).toHaveBeenCalledWith(note.id);
  });

  it("shows an error when note deletion fails", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockEmptyDocument();
    appMock.DeleteNote.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    const note = fakeNote({ title: "Title" });
    render(
      <I18nProvider>
        <NoteEditorPane {...baseProps({ note })} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.note_actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.delete_note" }));
    await user.click(await screen.findByRole("button", { name: "shell.delete_confirm_button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
  });

  it("creates a new note from the open note's menu", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockEmptyDocument();
    const note = fakeNote({ title: "Title" });
    const onCreateNote = vi.fn();
    render(
      <I18nProvider>
        <NoteEditorPane {...baseProps({ note, onCreateNote })} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.note_actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.new_note_button" }));

    expect(onCreateNote).toHaveBeenCalled();
  });

  it("assigns and removes a tag on the open note", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockEmptyDocument();
    const note = fakeNote({ title: "Title" });
    const tag = { id: "tag-1", workspace_id: "ws-1", name: "urgent", deleted: false };
    const onToggleTag = vi.fn().mockResolvedValue(undefined);
    render(
      <I18nProvider>
        <NoteEditorPane {...baseProps({ note, tags: [tag], onToggleTag })} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "+ shell.tags_add_button" }));
    await user.click(await screen.findByRole("checkbox", { name: "urgent" }));

    expect(onToggleTag).toHaveBeenCalledWith(note.id, "tag-1", true);
  });
});

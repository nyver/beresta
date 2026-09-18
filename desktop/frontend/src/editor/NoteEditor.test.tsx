import { render, screen, waitFor } from "@testing-library/react";
import Quill, { Delta } from "quill";
import { describe, expect, it, vi } from "vitest";
import * as Y from "yjs";

import { I18nProvider } from "../i18n";
import { appMock, runtimeMock } from "../setupTests";
import { mockLocaleCatalog, mockSettings } from "../testUtils";
import { bytesToBase64 } from "./base64";
import { NoteEditor } from "./NoteEditor";

function mockDocument(text: string) {
  const doc = new Y.Doc();
  if (text) doc.getText("body").insert(0, text);
  const update = Y.encodeStateAsUpdate(doc);
  doc.destroy();
  appMock.GetNoteDocument.mockResolvedValue({ update_base64: bytesToBase64(update), format: "v1" });
}

/** Waits for NoteEditor to mount, then returns its live Quill instance.
 * Quill.find keys instances by the original container element (which it
 * takes over and adds the ql-container class to), not the editable
 * .ql-editor child, so this looks it up by that ancestor. */
async function findQuill(): Promise<Quill> {
  const editor = await waitFor(() => {
    const element = document.querySelector(".ql-editor");
    if (!element) throw new Error("editor not mounted yet");
    return element as HTMLElement;
  });
  const container = editor.closest(".ql-container");
  if (!container) throw new Error("Quill container not mounted yet");
  return Quill.find(container) as Quill;
}

describe("NoteEditor", () => {
  it("hydrates Quill with the note's existing body text", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockDocument("hello world");

    render(
      <I18nProvider>
        <NoteEditor noteId="note-1" />
      </I18nProvider>,
    );

    await waitFor(() => expect(document.querySelector(".ql-editor")).toHaveTextContent("hello world"));
  });

  it("forwards a pasted image to onAttachFiles instead of inserting it inline", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockDocument("");
    const onAttachFiles = vi.fn();

    render(
      <I18nProvider>
        <NoteEditor noteId="note-1" onAttachFiles={onAttachFiles} />
      </I18nProvider>,
    );
    const editor = await waitFor(() => {
      const element = document.querySelector(".ql-editor");
      if (!element) throw new Error("editor not mounted yet");
      return element as HTMLElement;
    });

    const file = new File(["fake image bytes"], "screenshot.png", { type: "image/png" });
    const pasteEvent = new Event("paste", { bubbles: true, cancelable: true }) as ClipboardEvent;
    Object.defineProperty(pasteEvent, "clipboardData", { value: { files: [file] } });
    editor.dispatchEvent(pasteEvent);

    expect(onAttachFiles).toHaveBeenCalledWith([file]);
    expect(editor).toHaveTextContent("");
  });

  it("degrades pasted formatting outside the canonical set instead of silently keeping it", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockDocument("");

    render(
      <I18nProvider>
        <NoteEditor noteId="note-1" />
      </I18nProvider>,
    );
    // Exercises the same Quill instance and clipboard matcher pipeline a
    // real paste event would (see NoteEditor's clipboard module config),
    // through Quill's own public convert() API rather than a simulated
    // DOM paste event, since jsdom does not implement enough of
    // Selection/Range for Quill's paste handler to run end to end.
    const quill = await findQuill();
    const converted = quill.clipboard.convert({
      html: '<h4>Too deep</h4><p><u>underlined</u> and <span style="color:red">colored</span> and <b>bold</b></p>',
    });

    expect(converted.ops).not.toContainEqual(expect.objectContaining({ attributes: expect.objectContaining({ header: 4 }) }));
    for (const op of converted.ops) {
      for (const key of Object.keys(op.attributes ?? {})) {
        expect(["bold", "italic", "strike", "code", "header", "list", "blockquote", "code-block", "link"]).toContain(
          key,
        );
      }
    }
    // The degradation drops formatting, never the underlying text.
    const text = converted.ops.map((op) => (typeof op.insert === "string" ? op.insert : "")).join("");
    expect(text).toContain("Too deep");
    expect(text).toContain("underlined");
    expect(text).toContain("colored");
    expect(text).toContain("bold");
  });

  it("keeps typing normally after a degraded paste (re-edit)", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockDocument("");

    render(
      <I18nProvider>
        <NoteEditor noteId="note-1" />
      </I18nProvider>,
    );
    const quill = await findQuill();

    // The same conversion a real paste would run through (see the
    // "degrades pasted formatting" test above), inserted the way Quill's
    // own paste handler ultimately applies a converted delta: through
    // updateContents, from a real user action.
    const converted = quill.clipboard.convert({ html: "<h4>Too deep</h4>" });
    quill.updateContents(converted, "user");
    quill.updateContents(new Delta().retain(quill.getLength() - 1).insert(" continued"), "user");

    expect(quill.getText()).toBe("Too deep continued\n");
  });

  it("undo/redo round-trips a formatted edit within the canonical format set", async () => {
    mockLocaleCatalog();
    mockSettings();
    mockDocument("");

    render(
      <I18nProvider>
        <NoteEditor noteId="note-1" />
      </I18nProvider>,
    );
    const quill = await findQuill();

    // setContents replaces the document outright, including the lone "\n"
    // a fresh Quill document already starts with - unlike updateContents,
    // which would compose on top of it and leave two trailing newlines.
    quill.setContents(new Delta().insert("Hello ").insert("bold", { bold: true }).insert("\n"), "user");
    // cutoff() starts a new undo group immediately instead of relying on
    // history's default 1s merge window, so this second edit undoes on
    // its own.
    quill.history.cutoff();
    quill.updateContents(new Delta().retain(quill.getLength() - 1).insert(" more"), "user");

    const fullyEdited = quill.getContents();
    expect(quill.getText()).toBe("Hello bold more\n");

    quill.history.undo();
    expect(quill.getText()).toBe("Hello bold\n");
    // The formatting from the first (not-undone) edit survives the undo.
    expect(quill.getContents().ops).toContainEqual({ insert: "bold", attributes: { bold: true } });

    quill.history.redo();
    expect(quill.getContents()).toEqual(fullyEdited);
  });

  // Covers task 6.2's "Remote merge during editing" scenario (specs/
  // notes-management/spec.md): the cursor/selection preservation itself is
  // Quill's own Delta-transform (y-quill applies a remote Yjs change via
  // updateContents, never setContents - see useNoteDocument's
  // applyRemoteMerge doc comment); what this test actually proves is that
  // this codebase's wiring delivers a background merge to an *already
  // open* editor through the live Y.Doc at all, rather than the editor
  // silently going stale until the note is reopened.
  it("keeps the cursor anchored to its referenced content across a background merge", async () => {
    mockLocaleCatalog();
    mockSettings();
    const initialDoc = new Y.Doc();
    initialDoc.getText("body").insert(0, "Hello world");
    const initialUpdate = Y.encodeStateAsUpdate(initialDoc);
    appMock.GetNoteDocument.mockResolvedValueOnce({ update_base64: bytesToBase64(initialUpdate), format: "v1" });

    render(
      <I18nProvider>
        <NoteEditor noteId="note-1" />
      </I18nProvider>,
    );
    const quill = await findQuill();
    await waitFor(() => expect(quill.getText()).toBe("Hello world\n"));

    // The cursor sits right after "world", before the trailing newline.
    quill.setSelection(11, 0, "silent");
    expect(quill.getSelection()?.index).toBe(11);

    // A remote client that forked from the same base state (before this
    // session ever loaded it - matching a merge that happened on another
    // device) inserts text at the very start of the document.
    const remoteDoc = new Y.Doc();
    Y.applyUpdate(remoteDoc, initialUpdate);
    remoteDoc.getText("body").insert(0, "PREFIX ");
    const remoteUpdate = Y.encodeStateAsUpdate(remoteDoc);
    remoteDoc.destroy();
    appMock.GetNoteDocument.mockResolvedValueOnce({ update_base64: bytesToBase64(remoteUpdate), format: "v1" });

    const [, onSyncSummary] =
      runtimeMock.EventsOnMultiple.mock.calls.find(([name]) => name === "sync:summary") ?? [];
    if (!onSyncSummary) throw new Error("NoteEditor never subscribed to sync:summary");
    onSyncSummary();

    await waitFor(() => expect(quill.getText()).toBe("PREFIX Hello world\n"));
    // The cursor's *referenced content* (still right after "world") is
    // what must survive, not the raw index - 7 characters were inserted
    // ahead of it, so the index shifts by exactly that much.
    expect(quill.getSelection()?.index).toBe(11 + "PREFIX ".length);
  });

  it("shows a localized error when the document fails to load", async () => {
    mockLocaleCatalog();
    mockSettings();
    appMock.GetNoteDocument.mockRejectedValue(
      new Error(JSON.stringify({ code: "not_found", message: "note not found" })),
    );

    render(
      <I18nProvider>
        <NoteEditor noteId="missing-note" />
      </I18nProvider>,
    );

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.not_found");
  });
});

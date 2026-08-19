import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import * as Y from "yjs";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
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

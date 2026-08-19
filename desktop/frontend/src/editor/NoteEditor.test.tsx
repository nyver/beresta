import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
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

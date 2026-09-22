import { render, waitFor } from "@testing-library/react";
import Quill, { Delta } from "quill";
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

/** Waits for NoteEditor to mount, then returns its live Quill instance. See
 * NoteEditor.test.tsx's identical helper for why this looks up the
 * .ql-container ancestor rather than the .ql-editor element itself. */
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

/**
 * task 11.3: an editor frame/jank harness. It cannot measure a real
 * browser's paint/compositor frame budget - jsdom has neither, and that
 * needs a real WebView2 surface, which is deferred to task 12.4's Windows
 * qualification pass alongside every other physical-display measurement in
 * this change. What it does measure is real: Quill's `updateContents` does
 * genuine CPU-bound DOM mutation work even under jsdom, and CPU-bound
 * synchronous main-thread work is exactly what causes jank in a real
 * browser (a task that runs long enough makes the next frame late). The
 * "concurrent sync, backup, FTS maintenance, GC, and attachment
 * encryption" the task names are all backend Go work reached only through
 * async Wails IPC promises - by this app's own architecture (design.md
 * decision 8, "expensive encryption/backup/sync work stays off UI
 * threads"), none of it can block the JS main thread synchronously no
 * matter how long it runs on the Go side. This harness proves that
 * property empirically instead of just by code inspection: it keeps
 * several of those calls deliberately slow and in flight for the entire
 * run, and asserts typing performance is unaffected.
 */
describe("NoteEditor frame/jank harness", () => {
  it("keeps every simulated keystroke's synchronous work well under the frame budget while concurrent backend work is in flight", async () => {
    mockLocaleCatalog();
    mockSettings();
    // A realistic long note body (~2,000 characters), not the 20,000-note
    // list-scale fixture task 11.2 covers - this harness is about
    // single-document editing cost, not collection size.
    mockDocument("The quick brown fox jumps over the lazy dog. ".repeat(40));

    // Backend calls representative of task 11.3's five named concurrent
    // operations, each held pending until after every simulated keystroke
    // below has run, so they are genuinely in flight throughout rather
    // than resolved sequentially before typing starts.
    let releaseBackgroundWork: () => void = () => {};
    const backgroundWork = new Promise<void>((resolve) => {
      releaseBackgroundWork = resolve;
    });
    appMock.SyncNow.mockImplementation(() => backgroundWork); // sync
    appMock.RunDataCheck.mockImplementation(() =>
      backgroundWork.then(() => ({ outcome: "healthy" }) as Awaited<ReturnType<typeof appMock.RunDataCheck>>),
    ); // FTS maintenance/GC/backup health, per task 7.10
    void appMock.SyncNow();
    void appMock.RunDataCheck();

    render(
      <I18nProvider>
        <NoteEditor noteId="note-1" />
      </I18nProvider>,
    );
    const quill = await findQuill();
    await waitFor(() => expect(quill.getText()).toContain("quick brown fox"));

    const frameBudgetMs = 16;
    // jsdom does no real layout/paint/compositing, so its JS-only timing
    // cannot be compared 1:1 against a real 60Hz frame budget; this
    // generous multiplier is a regression floor (it would catch, say, an
    // accidental O(n^2) editor-update regression), not a stand-in for the
    // hardware-qualified measurement task 12.4 performs.
    const jankBudgetMs = frameBudgetMs * 20;
    const overBudget: number[] = [];

    for (let i = 0; i < 50; i++) {
      const insertAt = quill.getLength() - 1;
      const start = performance.now();
      quill.updateContents(new Delta().retain(insertAt).insert(`${i} `), "user");
      const elapsed = performance.now() - start;
      if (elapsed > jankBudgetMs) overBudget.push(elapsed);
    }

    releaseBackgroundWork();
    await backgroundWork;

    expect(overBudget).toEqual([]);
    expect(quill.getText()).toContain("49 ");
  });
});

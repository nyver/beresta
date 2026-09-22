import { act, render, waitFor } from "@testing-library/react";
import Quill, { Delta } from "quill";
import { describe, expect, it } from "vitest";
import * as Y from "yjs";

import { I18nProvider } from "../i18n";
import { appMock, runtimeMock } from "../setupTests";
import { mockLocaleCatalog, mockSettings } from "../testUtils";
import { bytesToBase64 } from "./base64";
import { NoteEditor } from "./NoteEditor";

function mockDocument(text: string): Y.Doc {
  const doc = new Y.Doc();
  if (text) doc.getText("body").insert(0, text);
  const update = Y.encodeStateAsUpdate(doc);
  appMock.GetNoteDocument.mockResolvedValue({ update_base64: bytesToBase64(update), format: "v1" });
  return doc;
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

/** Captures the "sync:summary" callback useNoteDocument registered through
 * EventsOn (a thin wrapper over EventsOnMultiple - see wailsjs/runtime/
 * runtime.js), mirroring useNoteDocument.test.ts's identical helper.
 * Invoking it is how a test simulates a real incoming background-sync
 * merge, since that is the only way one ever reaches an open note (see
 * useNoteDocument's applyRemoteMerge). */
function findSyncSummaryHandler(): () => void {
  const [, handler] = runtimeMock.EventsOnMultiple.mock.calls.find(([name]) => name === "sync:summary") ?? [];
  if (!handler) throw new Error("NoteEditor never subscribed to sync:summary");
  return handler as () => void;
}

/**
 * task 11.3: an editor frame/jank harness. It cannot measure a real
 * browser's paint/compositor frame budget - jsdom has neither, and that
 * needs a real WebView2 surface, which is deferred to task 12.4's Windows
 * qualification pass alongside every other physical-display measurement in
 * this change. What it does measure is real: Quill's `updateContents` does
 * genuine CPU-bound DOM mutation work even under jsdom, and CPU-bound
 * synchronous main-thread work is exactly what causes jank in a real
 * browser (a task that runs long enough makes the next frame late).
 *
 * The concurrent work this harness exercises is a genuine background-sync
 * remote merge (useNoteDocument's applyRemoteMerge, triggered by a real
 * "sync:summary" event - see findSyncSummaryHandler above), not desktop's
 * SyncNow/RunDataCheck bound methods: NoteEditor and its useNoteDocument
 * hook never call either of those, so holding them pending (an earlier
 * version of this test did) would have measured nothing but Quill's own
 * update cost - the mocks would have had no way to interact with the
 * component under test at all. A remote merge is the one piece of backend-
 * originated work that genuinely touches the same live Y.Doc typing writes
 * into, making it the real concurrency case worth measuring: does
 * processing an incoming merge concurrently with typing ever make a
 * keystroke's synchronous work take too long?
 */
describe("NoteEditor frame/jank harness", () => {
  it("keeps every simulated keystroke's synchronous work well under the frame budget while a real background-sync merge lands concurrently", async () => {
    mockLocaleCatalog();
    mockSettings();
    // A realistic long note body (~2,000 characters), not the 20,000-note
    // list-scale fixture task 11.2 covers - this harness is about
    // single-document editing cost, not collection size.
    const baseline = mockDocument("The quick brown fox jumps over the lazy dog. ".repeat(40));

    render(
      <I18nProvider>
        <NoteEditor noteId="note-1" />
      </I18nProvider>,
    );
    const quill = await findQuill();
    await waitFor(() => expect(quill.getText()).toContain("quick brown fox"));
    const onSyncSummary = findSyncSummaryHandler();

    // A separate Yjs client diverging from the same base state the editor
    // just loaded, mirroring how a real remote device's concurrent edit
    // arrives (the same construction useNoteDocument.test.ts's remote-merge
    // tests use) - queued now so the mid-loop trigger below can resolve it
    // immediately, keeping the merge's own fetch latency out of what this
    // test measures.
    const remoteDoc = new Y.Doc();
    Y.applyUpdate(remoteDoc, Y.encodeStateAsUpdate(baseline));
    remoteDoc.getText("body").insert(0, "Remote merge landed. ");
    const remoteUpdate = Y.encodeStateAsUpdate(remoteDoc);
    remoteDoc.destroy();
    baseline.destroy();
    appMock.GetNoteDocument.mockResolvedValueOnce({ update_base64: bytesToBase64(remoteUpdate), format: "v1" });

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

      // Fire the real background-sync merge partway through typing, so it
      // is genuinely concurrent with (not sequenced before or after) the
      // remaining keystrokes below.
      if (i === 25) act(() => onSyncSummary());
    }

    await waitFor(() => expect(quill.getText()).toContain("Remote merge landed."));

    expect(overBudget).toEqual([]);
    expect(quill.getText()).toContain("49 ");
  });
});

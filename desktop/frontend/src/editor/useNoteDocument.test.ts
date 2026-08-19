import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as Y from "yjs";

import { appMock } from "../setupTests";
import { bytesToBase64 } from "./base64";
import { useNoteDocument } from "./useNoteDocument";

function emptyDocumentResponse() {
  const doc = new Y.Doc();
  const update = Y.encodeStateAsUpdate(doc);
  doc.destroy();
  return { update_base64: bytesToBase64(update), format: "v1" };
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("useNoteDocument", () => {
  it("hydrates a Y.Doc from the fetched state and becomes ready", async () => {
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    const { result } = renderHook(() => useNoteDocument("note-1"));

    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    expect(result.current.ready).toBe(true);
    expect(result.current.ydoc).not.toBeNull();
    expect(result.current.error).toBeNull();
  });

  it("commits a debounced merged update after a local edit", async () => {
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    appMock.CommitNoteBody.mockResolvedValue(undefined);
    const { result } = renderHook(() => useNoteDocument("note-1"));

    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });
    expect(result.current.ready).toBe(true);

    act(() => {
      result.current.ydoc!.getText("body").insert(0, "hello");
    });
    expect(appMock.CommitNoteBody).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(800);
    });

    expect(appMock.CommitNoteBody).toHaveBeenCalledTimes(1);
    const call = appMock.CommitNoteBody.mock.calls[0][0];
    expect(call.note_id).toBe("note-1");
    expect(call.update_format).toBe("v1");
    expect(call.title).toBeUndefined();
  });

  it("merges several rapid edits into a single commit", async () => {
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    appMock.CommitNoteBody.mockResolvedValue(undefined);
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    act(() => {
      result.current.ydoc!.getText("body").insert(0, "a");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(400);
    });
    act(() => {
      result.current.ydoc!.getText("body").insert(1, "b");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(800);
    });

    expect(appMock.CommitNoteBody).toHaveBeenCalledTimes(1);
  });

  it("flush() commits immediately, bypassing the debounce", async () => {
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    appMock.CommitNoteBody.mockResolvedValue(undefined);
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    act(() => {
      result.current.ydoc!.getText("body").insert(0, "hello");
    });
    await act(async () => {
      await result.current.flush();
    });

    expect(appMock.CommitNoteBody).toHaveBeenCalledTimes(1);
  });

  it("flush(title) sends the full state when nothing else is pending", async () => {
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    appMock.CommitNoteBody.mockResolvedValue(undefined);
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    await act(async () => {
      await result.current.flush("New title");
    });

    expect(appMock.CommitNoteBody).toHaveBeenCalledTimes(1);
    const call = appMock.CommitNoteBody.mock.calls[0][0];
    expect(call.title).toBe("New title");
    expect(call.update_base64.length).toBeGreaterThan(0);
  });

  it("flush() is a no-op with nothing pending and no title", async () => {
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    await act(async () => {
      await result.current.flush();
    });

    expect(appMock.CommitNoteBody).not.toHaveBeenCalled();
  });

  it("reports a failed commit through error and keeps the edit queued for retry", async () => {
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    appMock.CommitNoteBody.mockRejectedValueOnce(
      new Error(JSON.stringify({ code: "internal", message: "disk full" })),
    );
    appMock.CommitNoteBody.mockResolvedValueOnce(undefined);
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    act(() => {
      result.current.ydoc!.getText("body").insert(0, "hello");
    });
    let firstAttempt: boolean | undefined;
    await act(async () => {
      firstAttempt = await result.current.flush();
    });
    expect(firstAttempt).toBe(false);
    expect(result.current.error?.code).toBe("internal");

    // Retrying (a second flush, e.g. from the next debounce) resends the
    // same edit rather than having silently dropped it.
    let secondAttempt: boolean | undefined;
    await act(async () => {
      secondAttempt = await result.current.flush();
    });
    expect(secondAttempt).toBe(true);
    expect(appMock.CommitNoteBody).toHaveBeenCalledTimes(2);
    expect(result.current.error).toBeNull();
  });

  it("retries a failed title-only rename's full-state payload on the next flush", async () => {
    // The exact gap this guards: a title-only flush has no pending delta
    // to fall back to on failure, so the retry payload must come from
    // somewhere other than the (empty) pending queue.
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    appMock.CommitNoteBody.mockRejectedValueOnce(
      new Error(JSON.stringify({ code: "internal", message: "disk full" })),
    );
    appMock.CommitNoteBody.mockResolvedValueOnce(undefined);
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    let firstAttempt: boolean | undefined;
    await act(async () => {
      firstAttempt = await result.current.flush("New title");
    });
    expect(firstAttempt).toBe(false);

    // A later flush, even one that itself has no title, must still resend
    // the previously failed rename payload instead of dropping it.
    let secondAttempt: boolean | undefined;
    await act(async () => {
      secondAttempt = await result.current.flush();
    });
    expect(secondAttempt).toBe(true);
    expect(appMock.CommitNoteBody).toHaveBeenCalledTimes(2);
    expect(appMock.CommitNoteBody.mock.calls[1][0].update_base64.length).toBeGreaterThan(0);
  });

  it("flushes pending edits on unmount", async () => {
    // Real timers here: the assertion below waits on the unmount
    // cleanup's fire-and-forget flush() promise settling, which
    // testing-library's waitFor polls for using real setTimeout - under
    // fake timers that polling never advances on its own and the test
    // hangs until Vitest's own timeout kills it.
    vi.useRealTimers();
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    appMock.CommitNoteBody.mockResolvedValue(undefined);
    const { result, unmount } = renderHook(() => useNoteDocument("note-1"));
    await waitFor(() => expect(result.current.ready).toBe(true));

    act(() => {
      result.current.ydoc!.getText("body").insert(0, "hello");
    });
    unmount();

    await waitFor(() => expect(appMock.CommitNoteBody).toHaveBeenCalledTimes(1));
  });

  it("re-hydrates from scratch when noteId changes", async () => {
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    const { result, rerender } = renderHook(({ noteId }) => useNoteDocument(noteId), {
      initialProps: { noteId: "note-1" },
    });
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });
    const firstDoc = result.current.ydoc;

    rerender({ noteId: "note-2" });
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    expect(result.current.ydoc).not.toBe(firstDoc);
    expect(appMock.GetNoteDocument).toHaveBeenCalledWith("note-2");
  });
});

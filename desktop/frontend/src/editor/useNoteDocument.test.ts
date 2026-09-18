import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as Y from "yjs";

import { appMock, runtimeMock } from "../setupTests";
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

  it("starts with a null saveState and reports saving/saved around a successful commit", async () => {
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    let resolveCommit: (() => void) | undefined;
    appMock.CommitNoteBody.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveCommit = resolve;
        }),
    );
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });
    expect(result.current.saveState).toBeNull();

    act(() => {
      result.current.ydoc!.getText("body").insert(0, "hello");
    });
    let flushPromise!: Promise<boolean>;
    act(() => {
      flushPromise = result.current.flush();
    });
    expect(result.current.saveState).toBe("saving");

    await act(async () => {
      resolveCommit?.();
      await flushPromise;
    });
    expect(result.current.saveState).toBe("saved");
  });

  it("reports could_not_save (not saved) from a failed commit, then saved on retry", async () => {
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
    await act(async () => {
      await result.current.flush();
    });
    expect(result.current.saveState).toBe("could_not_save");

    await act(async () => {
      await result.current.flush();
    });
    expect(result.current.saveState).toBe("saved");
  });

  // The end-to-end version of core/editorcommit/tracker_test.go's stale-
  // completion guarantee: two commits end up in flight (an explicit flush
  // while an earlier debounced-equivalent flush has not yet resolved),
  // the newer one resolves first, and the older one's later completion
  // must not change what is already displayed for the newer content.
  it("does not let a stale completion overwrite the status newer dirty content already reported", async () => {
    appMock.GetNoteDocument.mockResolvedValue(emptyDocumentResponse());
    let resolveFirst: (() => void) | undefined;
    let resolveSecond: (() => void) | undefined;
    appMock.CommitNoteBody.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveFirst = resolve;
        }),
    ).mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveSecond = resolve;
        }),
    );
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    act(() => {
      result.current.ydoc!.getText("body").insert(0, "a");
    });
    let firstFlush!: Promise<boolean>;
    act(() => {
      firstFlush = result.current.flush();
    });

    act(() => {
      result.current.ydoc!.getText("body").insert(1, "b");
    });
    let secondFlush!: Promise<boolean>;
    act(() => {
      secondFlush = result.current.flush();
    });

    expect(appMock.CommitNoteBody).toHaveBeenCalledTimes(2);

    await act(async () => {
      resolveSecond?.();
      await secondFlush;
    });
    expect(result.current.saveState).toBe("saved");

    await act(async () => {
      resolveFirst?.();
      await firstFlush;
    });
    expect(result.current.saveState).toBe("saved");
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

  // The two tests below cover task 6.2's "Remote merge during editing"
  // scenario (specs/notes-management/spec.md): a background sync merges a
  // remote change into a note's durable state, delivered to an already-
  // open note only through the "sync:summary" signal SyncPanel/Shell
  // already use (see useNoteDocument's applyRemoteMerge) - simulated here
  // exactly like SyncPanel.test.tsx does, by capturing and directly
  // invoking the callback EventsOnMultiple was registered with.
  function findSyncSummaryHandler(): () => void {
    const [, handler] = runtimeMock.EventsOnMultiple.mock.calls.find(([name]) => name === "sync:summary") ?? [];
    if (!handler) throw new Error("useNoteDocument never subscribed to sync:summary");
    return handler as () => void;
  }

  it("merges a remote sync update into the live doc without queuing it for local commit", async () => {
    // Real timers: this test's waitFor polls for a promise chain
    // (applyRemoteMerge's getNoteDocument().then(...)) with nothing to
    // advance under fake timers, exactly like "flushes pending edits on
    // unmount" below.
    vi.useRealTimers();
    appMock.GetNoteDocument.mockResolvedValueOnce(emptyDocumentResponse());
    appMock.CommitNoteBody.mockResolvedValue(undefined);
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await waitFor(() => expect(result.current.ready).toBe(true));
    const onSyncSummary = findSyncSummaryHandler();

    // A separate Yjs client diverging from the same base state the local
    // doc just loaded, mirroring how a real remote device's change
    // arrives - not the same object as result.current.ydoc.
    const remoteDoc = new Y.Doc();
    Y.applyUpdate(remoteDoc, Y.encodeStateAsUpdate(result.current.ydoc!));
    remoteDoc.getText("body").insert(0, "from another device");
    const remoteUpdate = Y.encodeStateAsUpdate(remoteDoc);
    remoteDoc.destroy();
    appMock.GetNoteDocument.mockResolvedValueOnce({ update_base64: bytesToBase64(remoteUpdate), format: "v1" });

    act(() => onSyncSummary());

    await waitFor(() => expect(result.current.ydoc!.getText("body").toString()).toBe("from another device"));
    // The merge is already durable server-side (that is where it came
    // from); re-queuing it for commit would be redundant and would wrongly
    // mark this session as edited.
    expect(appMock.CommitNoteBody).not.toHaveBeenCalled();
    expect(result.current.everEdited).toBe(false);
  });

  it("coalesces a burst of sync:summary ticks into one outstanding refresh plus one trailing catch-up", async () => {
    // "sync:summary" fires once per pull page/apply/push batch
    // (desktop/sync.go's Progress callback), so a device catching up
    // after being offline can fire many ticks within milliseconds; this
    // proves that burst never queues one GetNoteDocument call per tick.
    vi.useRealTimers();
    appMock.GetNoteDocument.mockResolvedValueOnce(emptyDocumentResponse());
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await waitFor(() => expect(result.current.ready).toBe(true));
    const onSyncSummary = findSyncSummaryHandler();
    expect(appMock.GetNoteDocument).toHaveBeenCalledTimes(1);

    let resolveFirstMerge: ((value: { update_base64: string; format: string }) => void) | undefined;
    appMock.GetNoteDocument.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveFirstMerge = resolve;
        }),
    );

    // Three ticks in a row while the first refresh they trigger is still
    // pending.
    act(() => {
      onSyncSummary();
      onSyncSummary();
      onSyncSummary();
    });
    // Only the first tick's fetch actually went out; the other two were
    // coalesced into a single pending "refresh again after this" flag
    // instead of each starting their own request.
    expect(appMock.GetNoteDocument).toHaveBeenCalledTimes(2);

    appMock.GetNoteDocument.mockResolvedValueOnce(emptyDocumentResponse());
    resolveFirstMerge?.(emptyDocumentResponse());

    // Exactly one trailing catch-up call fires once the in-flight one
    // settles - not two more for the two ticks that arrived during it.
    await waitFor(() => expect(appMock.GetNoteDocument).toHaveBeenCalledTimes(3));
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(appMock.GetNoteDocument).toHaveBeenCalledTimes(3);
  });

  it("keeps an uncommitted local edit when a remote merge arrives concurrently", async () => {
    vi.useRealTimers();
    appMock.GetNoteDocument.mockResolvedValueOnce(emptyDocumentResponse());
    appMock.CommitNoteBody.mockResolvedValue(undefined);
    const { result } = renderHook(() => useNoteDocument("note-1"));
    await waitFor(() => expect(result.current.ready).toBe(true));
    const onSyncSummary = findSyncSummaryHandler();

    // The common state both writers started from, captured before either
    // one's edit below - true concurrent divergence, not one building on
    // the other's change.
    const baseState = Y.encodeStateAsUpdate(result.current.ydoc!);

    // The user has typed something that has not been committed yet -
    // still only in this local Y.Doc, exactly like the pending-edit tests
    // above.
    act(() => {
      result.current.ydoc!.getText("body").insert(0, "local draft");
    });

    // A remote change from a separate client that forked from the same
    // base state and never saw the local edit above.
    const remoteDoc = new Y.Doc();
    Y.applyUpdate(remoteDoc, baseState);
    remoteDoc.getText("body").insert(0, "remote change");
    const remoteUpdate = Y.encodeStateAsUpdate(remoteDoc);
    remoteDoc.destroy();
    appMock.GetNoteDocument.mockResolvedValueOnce({ update_base64: bytesToBase64(remoteUpdate), format: "v1" });

    act(() => onSyncSummary());

    // Yjs converges deterministically; this test only needs both writers'
    // content to survive the merge - a torn or one-sided result is what it
    // guards against - not the exact interleaving.
    await waitFor(() => {
      const merged = result.current.ydoc!.getText("body").toString();
      expect(merged).toContain("local draft");
      expect(merged).toContain("remote change");
    });

    // The still-uncommitted local edit is not lost or silently dropped by
    // the merge: it is still queued and reaches the server on the next
    // flush.
    await act(async () => {
      await result.current.flush();
    });
    expect(appMock.CommitNoteBody).toHaveBeenCalledTimes(1);
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

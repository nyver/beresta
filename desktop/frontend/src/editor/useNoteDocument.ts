import { useCallback, useEffect, useRef, useState } from "react";
import * as Y from "yjs";

import { commitNoteBody, getNoteDocument, unwrapError, type ApiError } from "../api";
import { base64ToBytes, bytesToBase64 } from "./base64";
import { CommitTracker, type LocalSaveState } from "./commitTracker";

/** How long an edit waits, with no further edits, before it is committed to
 * the Go core. Chosen to keep keystrokes from each individually round-
 * tripping through IPC while still saving promptly if the user pauses. */
const COMMIT_DEBOUNCE_MS = 800;

export interface NoteDocumentState {
  ydoc: Y.Doc | null;
  ready: boolean;
  error: ApiError | null;
  /** The closed local-save state for the status line (see
   * shell/SaveStatusLine.tsx and specs/product-experience's "Note
   * persistence SHALL be represented only as Saving, Saved, or Could not
   * save"), or null before this note session's first commit attempt -
   * the UI treats that the same as "saved", since there is nothing
   * pending or at risk yet. A commit completion updates this only when
   * CommitTracker confirms it is not stale - see commitTracker.ts. */
  saveState: LocalSaveState | null;
  /**
   * flush commits any pending body edits (merged into one update) right
   * now, bypassing the debounce, optionally renaming the note in the same
   * commit. Safe to call with nothing pending: it is then a no-op unless
   * title is given, in which case it sends the document's full current
   * state as an idempotent one-off update (CommitNoteBody rejects an
   * empty update, and title changes have no operation encoding of their
   * own - see core/sync's NoteMetadataOperation kinds, none of which is
   * "title"). Never throws: failures are reported through the returned
   * boolean *and* through `error`, and the attempted payload (delta or
   * full-state alike) is kept pending for the next flush to retry -
   * callers that treat a rename or edit as durably saved (for example,
   * updating a note list's displayed title) must check the return value
   * first.
   */
  flush: (title?: string) => Promise<boolean>;
}

export function useNoteDocument(noteId: string): NoteDocumentState {
  const [ydoc, setYdoc] = useState<Y.Doc | null>(null);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [saveState, setSaveState] = useState<LocalSaveState | null>(null);

  const pendingRef = useRef<Uint8Array[]>([]);
  const timerRef = useRef<number | undefined>(undefined);
  const ydocRef = useRef<Y.Doc | null>(null);
  const noteIdRef = useRef(noteId);
  // One CommitTracker per open note session: a fresh instance each time
  // noteId changes (reset below), matching one generation sequence per
  // editor session as design.md decision 2 describes.
  const trackerRef = useRef(new CommitTracker());

  const flush = useCallback(async (title?: string): Promise<boolean> => {
    window.clearTimeout(timerRef.current);
    const doc = ydocRef.current;
    if (!doc) return true;

    // Take ownership of everything queued so far and replace the shared
    // buffer with a fresh array *before* awaiting anything: doc.on("update")
    // keeps firing (and pushing) for edits made while this commit is in
    // flight, and those must land in the new array, not get silently
    // dropped by an accidental array-identity mix-up with what we're about
    // to send.
    const sending = pendingRef.current;
    pendingRef.current = [];

    let payload: Uint8Array;
    if (sending.length > 0) {
      payload = sending.length === 1 ? sending[0] : Y.mergeUpdates(sending);
    } else if (title !== undefined) {
      // No pending delta but a rename still needs a non-empty update (see
      // this function's doc comment); the full state is idempotent to
      // re-apply, unlike an incremental delta would be if replayed twice.
      payload = Y.encodeStateAsUpdate(doc);
    } else {
      return true;
    }

    // The generation this commit represents: whatever dirty() calls (from
    // doc.on("update") below) have accumulated up to this exact moment.
    // Captured before awaiting so a completion can later be told apart
    // from any newer generation that starts (and captures a higher
    // number) while this commit is still in flight.
    const generation = trackerRef.current.current();
    setSaveState("saving");
    try {
      await commitNoteBody({
        note_id: noteIdRef.current,
        update_base64: bytesToBase64(payload),
        update_format: "v1",
        title,
      });
      // A stale completion (superseded by newer dirty input that arrived
      // after this commit started) is discarded here: it must never
      // downgrade the status newer, still-unsaved input already reports,
      // even though this older commit itself succeeded.
      const accepted = trackerRef.current.accept(generation, true);
      if (accepted) {
        setError(null);
        setSaveState(accepted);
      }
      return true;
    } catch (thrown) {
      // Put the attempted payload back ahead of anything queued in the
      // meantime, so the next flush retries it instead of silently
      // dropping the edit - this applies equally to the title-only
      // full-state fallback above, which is just as safe to resend later
      // (merged with whatever else has since queued) as a normal delta.
      // This happens regardless of staleness: a superseded generation's
      // failed bytes are still real, not-yet-durable content that must
      // not be lost, even though the status line will not show an error
      // for them (see the discard comment above).
      pendingRef.current = [payload, ...pendingRef.current];
      const accepted = trackerRef.current.accept(generation, false);
      if (accepted) {
        setError(unwrapError(thrown));
        setSaveState(accepted);
      }
      return false;
    }
  }, []);

  useEffect(() => {
    noteIdRef.current = noteId;
    let canceled = false;
    setReady(false);
    setError(null);
    setSaveState(null);
    pendingRef.current = [];
    trackerRef.current = new CommitTracker();
    window.clearTimeout(timerRef.current);

    getNoteDocument(noteId)
      .then(({ update_base64, format }) => {
        if (canceled) return;
        const doc = new Y.Doc();
        const bytes = base64ToBytes(update_base64);
        if (bytes.length > 0) {
          if (format === "v2") {
            Y.applyUpdateV2(doc, bytes);
          } else {
            Y.applyUpdate(doc, bytes);
          }
        }
        doc.on("update", (update: Uint8Array) => {
          pendingRef.current.push(update);
          trackerRef.current.dirty();
          window.clearTimeout(timerRef.current);
          timerRef.current = window.setTimeout(() => {
            void flush();
          }, COMMIT_DEBOUNCE_MS);
        });
        ydocRef.current = doc;
        setYdoc(doc);
        setReady(true);
      })
      .catch((thrown: unknown) => {
        if (!canceled) setError(unwrapError(thrown));
      });

    return () => {
      canceled = true;
      void flush();
      window.clearTimeout(timerRef.current);
      ydocRef.current?.destroy();
      ydocRef.current = null;
      setYdoc(null);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [noteId]);

  return { ydoc, ready, error, flush, saveState };
}

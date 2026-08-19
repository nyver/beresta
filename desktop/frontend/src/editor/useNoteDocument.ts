import { useCallback, useEffect, useRef, useState } from "react";
import * as Y from "yjs";

import { commitNoteBody, getNoteDocument, unwrapError, type ApiError } from "../api";
import { base64ToBytes, bytesToBase64 } from "./base64";

/** How long an edit waits, with no further edits, before it is committed to
 * the Go core. Chosen to keep keystrokes from each individually round-
 * tripping through IPC while still saving promptly if the user pauses. */
const COMMIT_DEBOUNCE_MS = 800;

export interface NoteDocumentState {
  ydoc: Y.Doc | null;
  ready: boolean;
  error: ApiError | null;
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

  const pendingRef = useRef<Uint8Array[]>([]);
  const timerRef = useRef<number | undefined>(undefined);
  const ydocRef = useRef<Y.Doc | null>(null);
  const noteIdRef = useRef(noteId);

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

    try {
      await commitNoteBody({
        note_id: noteIdRef.current,
        update_base64: bytesToBase64(payload),
        update_format: "v1",
        title,
      });
      setError(null);
      return true;
    } catch (thrown) {
      setError(unwrapError(thrown));
      // Put the attempted payload back ahead of anything queued in the
      // meantime, so the next flush retries it instead of silently
      // dropping the edit - this applies equally to the title-only
      // full-state fallback above, which is just as safe to resend later
      // (merged with whatever else has since queued) as a normal delta.
      pendingRef.current = [payload, ...pendingRef.current];
      return false;
    }
  }, []);

  useEffect(() => {
    noteIdRef.current = noteId;
    let canceled = false;
    setReady(false);
    setError(null);
    pendingRef.current = [];
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

  return { ydoc, ready, error, flush };
}

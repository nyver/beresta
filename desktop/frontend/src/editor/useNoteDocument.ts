import { useCallback, useEffect, useRef, useState } from "react";
import * as Y from "yjs";

import { commitNoteBody, getNoteDocument, recordPerfStage, unwrapError, type ApiError } from "../api";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { base64ToBytes, bytesToBase64 } from "./base64";
import { CommitTracker, type LocalSaveState } from "./commitTracker";

/** How long an edit waits, with no further edits, before it is committed to
 * the Go core. Chosen to keep keystrokes from each individually round-
 * tripping through IPC while still saving promptly if the user pauses. */
const COMMIT_DEBOUNCE_MS = 800;

/** Fires on every synchronization phase transition (see desktop/sync.go's
 * Progress callback); SyncPanel/Shell already use it as a cheap "recheck"
 * signal instead of a dedicated payload. Reused here for the same reason -
 * see the remote-merge subscription below. */
const EVENT_SYNC_SUMMARY = "sync:summary";

/** Tags a Y.Doc transaction as a background-synchronization merge (see the
 * remote-merge subscription below), so the doc.on("update") listener that
 * queues local edits for commit can tell it apart from the user's own
 * typing and not echo already-durable remote content back as a "local"
 * edit. */
const REMOTE_MERGE_ORIGIN = Symbol("remote-merge");

/** Applies a base64-encoded Yjs update, as GetNoteDocument's NoteDocumentDTO
 * returns it, to doc with the matching v1/v2 codec - shared by the initial
 * hydration and the remote-merge refresh below so the two never drift on
 * how "format" is interpreted. */
function applyEncodedUpdate(doc: Y.Doc, updateBase64: string, format: string, origin?: unknown): void {
  const bytes = base64ToBytes(updateBase64);
  if (bytes.length === 0) return;
  if (format === "v2") {
    Y.applyUpdateV2(doc, bytes, origin);
  } else {
    Y.applyUpdate(doc, bytes, origin);
  }
}

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
  /** True from the first local edit this note session (title or body
   * alike drive the tracker's generation - see the doc.on("update")
   * listener below), even before the debounce has committed it. Reset to
   * false whenever noteId changes. Used to tell an untouched fresh draft
   * apart from a note the user has actually started editing - see
   * NoteEditorPane's onDraftTouched. */
  everEdited: boolean;
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
  const [everEdited, setEverEdited] = useState(false);

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
    const commitStartedAtMs = performance.now();
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
        recordPerfStage("commit_acknowledged", commitStartedAtMs);
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
    const openStartedAtMs = performance.now();
    let canceled = false;
    let unsubscribeRemoteMerge: (() => void) | null = null;
    setReady(false);
    setError(null);
    setSaveState(null);
    setEverEdited(false);
    pendingRef.current = [];
    trackerRef.current = new CommitTracker();
    window.clearTimeout(timerRef.current);

    getNoteDocument(noteId)
      .then(({ update_base64, format }) => {
        if (canceled) return;
        const doc = new Y.Doc();
        applyEncodedUpdate(doc, update_base64, format);
        doc.on("update", (update: Uint8Array, origin: unknown) => {
          // A merge this same hook applied below (see
          // applyRemoteMerge) is already durable server-side; queuing it
          // back up for commit would be redundant at best and, worse,
          // would mark this session dirty/everEdited for content the user
          // never touched.
          if (origin === REMOTE_MERGE_ORIGIN) return;
          pendingRef.current.push(update);
          trackerRef.current.dirty();
          setEverEdited(true);
          window.clearTimeout(timerRef.current);
          timerRef.current = window.setTimeout(() => {
            void flush();
          }, COMMIT_DEBOUNCE_MS);
        });
        ydocRef.current = doc;
        setYdoc(doc);
        setReady(true);
        recordPerfStage("editor_ready", openStartedAtMs);

        // Background synchronization merges a remote change into this
        // note's durable state without ever touching this already-open
        // Y.Doc - there is no per-note server push, only local commits
        // (see design.md). "sync:summary" already fires on every sync
        // phase transition for the status line (desktop/sync.go), so
        // reusing it here as a cheap "maybe something changed, recheck"
        // signal needs no new Go-side plumbing: re-fetching and merging
        // an already-current note is a safe no-op (Yjs dedupes by
        // clock), and merging into the SAME live Y.Doc y-quill's
        // QuillBinding already observes - instead of replacing it - is
        // what lets Quill's own Delta transform keep the user's cursor
        // and selection stable through the merge (specs/notes-
        // management's "Remote merge during editing" scenario): that
        // transform is a Quill/y-quill property this relies on, not
        // something implemented here.
        // "sync:summary" can fire many times in a burst (once per pull
        // page, apply, and push batch - see desktop/sync.go's Progress
        // callback), most of which have nothing to do with this note; an
        // in-flight guard caps this at one outstanding GetNoteDocument
        // IPC call at a time instead of piling one up per tick, and a
        // single trailing "queued" flag (not a counter) still catches up
        // to the latest state once the in-flight fetch settles, rather
        // than silently dropping every tick that arrived meanwhile.
        let remoteMergeInFlight = false;
        let remoteMergeQueued = false;
        const applyRemoteMerge = () => {
          if (remoteMergeInFlight) {
            remoteMergeQueued = true;
            return;
          }
          remoteMergeInFlight = true;
          getNoteDocument(noteId)
            .then(({ update_base64: remoteBase64, format: remoteFormat }) => {
              if (canceled || ydocRef.current !== doc) return;
              applyEncodedUpdate(doc, remoteBase64, remoteFormat, REMOTE_MERGE_ORIGIN);
            })
            .catch(() => {
              // Best-effort: a failed background refresh leaves the
              // editor exactly as it was, nothing pending or at risk -
              // the next "sync:summary" tick (or reopening the note)
              // retries.
            })
            .finally(() => {
              remoteMergeInFlight = false;
              if (remoteMergeQueued && !canceled) {
                remoteMergeQueued = false;
                applyRemoteMerge();
              }
            });
        };
        unsubscribeRemoteMerge = EventsOn(EVENT_SYNC_SUMMARY, applyRemoteMerge);
      })
      .catch((thrown: unknown) => {
        if (!canceled) setError(unwrapError(thrown));
      });

    return () => {
      canceled = true;
      unsubscribeRemoteMerge?.();
      void flush();
      window.clearTimeout(timerRef.current);
      ydocRef.current?.destroy();
      ydocRef.current = null;
      setYdoc(null);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [noteId]);

  return { ydoc, ready, error, flush, saveState, everEdited };
}

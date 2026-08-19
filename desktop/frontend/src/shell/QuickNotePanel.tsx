import { useEffect, useRef, useState } from "react";

import { createNote, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { NoteEditor, type NoteEditorHandle } from "../editor/NoteEditor";
import { Modal } from "./Modal";

export interface QuickNotePanelProps {
  /** Called once the panel has finished closing (after any pending edit
   * has been flushed), so the caller can refresh its note list. */
  onClosed: () => void;
}

/**
 * QuickNotePanel is the "focused quick-note surface" the windows-desktop-
 * client spec's global-hotkey scenario asks for: a title field plus the
 * same Yjs-backed NoteEditor the main shell uses (task 5.4), filed at the
 * workspace root, with none of NoteEditorPane's attachment/revision
 * panels - just enough to capture a thought and get out of the way. It
 * creates the note immediately on mount rather than only on save, so
 * typing commits through the same debounced CommitNoteBody path as any
 * other note instead of needing a separate "new note" code path.
 */
export function QuickNotePanel({ onClosed }: QuickNotePanelProps) {
  const { t, errorMessage } = useI18n();
  const editorRef = useRef<NoteEditorHandle>(null);
  const [title, setTitle] = useState("");
  const [noteId, setNoteId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [closing, setClosing] = useState(false);

  useEffect(() => {
    let canceled = false;
    createNote("", "")
      .then((note) => {
        if (!canceled) setNoteId(note.id);
      })
      .catch((thrown: unknown) => {
        if (!canceled) setError(errorMessage(unwrapError(thrown)));
      });
    return () => {
      canceled = true;
    };
    // Deliberately runs once: QuickNotePanel is only ever mounted for one
    // capture session (App.tsx below remounts it via `key` for the next
    // one), so there is no notebookId/title dependency to react to here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // titleRef mirrors `title` so the unmount cleanup below always flushes
  // the latest typed value, not whatever it closed over when the effect
  // was set up.
  const titleRef = useRef(title);
  titleRef.current = title;

  useEffect(() => {
    return () => {
      // Guards against a typed title being silently lost when this panel
      // is torn down without going through handleClose's own flush - for
      // example a second quicknote:open session remounting it (Shell.tsx
      // bumps `key`) while a title has been typed but the body has not
      // been touched yet, so useNoteDocument's own unmount flush (body
      // only) never commits it. Safe to run even after handleClose
      // already flushed: NoteEditor's flush is a no-op with nothing
      // pending.
      void editorRef.current?.flush(titleRef.current.trim());
    };
  }, []);

  async function handleClose() {
    if (closing) return;
    setClosing(true);
    try {
      await editorRef.current?.flush(title.trim());
    } finally {
      onClosed();
    }
  }

  return (
    <Modal title={t("quicknote.title")} onClose={() => void handleClose()}>
      {error ? (
        <p className="error" role="alert">
          {error}
        </p>
      ) : (
        <div className="quicknote-panel">
          <input
            className="note-title-input"
            autoFocus
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            aria-label={t("shell.detail_title_label")}
            placeholder={t("shell.untitled_note")}
          />
          {noteId ? <NoteEditor ref={editorRef} noteId={noteId} /> : null}
          <div className="quicknote-actions">
            <button type="button" onClick={() => void handleClose()} disabled={closing}>
              {t("quicknote.done_button")}
            </button>
          </div>
        </div>
      )}
    </Modal>
  );
}

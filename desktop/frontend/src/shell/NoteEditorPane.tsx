import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";

import { NoteEditor, type NoteEditorHandle } from "../editor/NoteEditor";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface NoteEditorPaneHandle {
  /** Flushes the currently open note's pending body edit and any
   * not-yet-blurred title change. A no-op when no note is open. */
  flush: () => Promise<void>;
}

export interface NoteEditorPaneProps {
  note: main.NoteDTO | null;
  /** Called with the new title right after a rename has been durably
   * committed, so the caller can patch its own note list state. Never
   * called if the commit failed - see commitTitleIfChanged. */
  onTitleCommitted: (noteId: string, title: string) => void;
}

/**
 * NoteEditorPane owns the title field (a plain LWW metadata register, not
 * part of the note's Yjs body - see core/model.Note) alongside the Yjs
 * body editor, and coordinates committing both together.
 */
export const NoteEditorPane = forwardRef<NoteEditorPaneHandle, NoteEditorPaneProps>(
  function NoteEditorPane({ note, onTitleCommitted }, ref) {
    const { t } = useI18n();
    const editorRef = useRef<NoteEditorHandle>(null);
    const [title, setTitle] = useState(note?.title ?? "");

    useEffect(() => {
      setTitle(note?.title ?? "");
    }, [note?.id, note?.title]);

    async function commitTitleIfChanged() {
      if (!note) return;
      const trimmed = title.trim();
      if (trimmed === note.title) {
        await editorRef.current?.flush();
        return;
      }
      // useNoteDocument.flush never throws; a failed commit is reported
      // through this return value (and keeps the payload queued for the
      // next attempt) rather than through a rejection, specifically so a
      // caller that treats the result as "now persisted" - like patching
      // the note list's displayed title - cannot do so on a commit that
      // never actually landed. The failure itself is already surfaced to
      // the user by NoteEditor's own error banner (same underlying
      // useNoteDocument instance), so there is nothing further to show
      // here.
      const committed = await editorRef.current?.flush(trimmed);
      if (committed) {
        onTitleCommitted(note.id, trimmed);
      }
    }

    useImperativeHandle(ref, () => ({ flush: commitTitleIfChanged }), [note, title]);

    if (!note) {
      return (
        <div className="note-detail note-detail-empty">
          <p>{t("shell.detail_placeholder")}</p>
        </div>
      );
    }

    return (
      <div className="note-detail">
        <input
          className="note-title-input"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          onBlur={() => void commitTitleIfChanged()}
          aria-label={t("shell.detail_title_label")}
          placeholder={t("shell.untitled_note")}
        />
        <NoteEditor ref={editorRef} noteId={note.id} />
      </div>
    );
  },
);

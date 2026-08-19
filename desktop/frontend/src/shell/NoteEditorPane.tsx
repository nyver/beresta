import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";

import { NoteEditor, type NoteEditorHandle } from "../editor/NoteEditor";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { AttachmentPanel, type AttachmentPanelHandle } from "./AttachmentPanel";
import { RevisionsPanel } from "./RevisionsPanel";

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
    const attachmentPanelRef = useRef<AttachmentPanelHandle>(null);
    const [title, setTitle] = useState(note?.title ?? "");
    // Bumped after RestoreRevision commits a new current revision, and
    // included in NoteEditor's key below to force it to remount and
    // refetch the note's document: RestoreRevision writes through the
    // normal CommitNoteBody path, which the already-open Yjs document (an
    // in-memory structure with no server push) has no other way to learn
    // about.
    const [restoreVersion, setRestoreVersion] = useState(0);

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
        <NoteEditor
          key={`${note.id}-${restoreVersion}`}
          ref={editorRef}
          noteId={note.id}
          onAttachFiles={(files) => attachmentPanelRef.current?.attachFiles(files)}
        />
        <AttachmentPanel key={note.id} ref={attachmentPanelRef} noteId={note.id} />
        <RevisionsPanel
          key={`revisions-${note.id}`}
          noteId={note.id}
          onBeforeRestore={async () => {
            // Flush any not-yet-debounced body edit into its own revision
            // first: RestoreRevision below unconditionally replaces the
            // note's current content, but if a pending edit stayed queued
            // instead, remounting NoteEditor after the restore (via
            // restoreVersion below) would tear down the old instance and
            // its useNoteDocument cleanup would flush that stale edit on
            // top of the just-restored content - silently reintroducing
            // exactly what the user asked to discard.
            await editorRef.current?.flush();
          }}
          onRestored={() => setRestoreVersion((v) => v + 1)}
        />
      </div>
    );
  },
);

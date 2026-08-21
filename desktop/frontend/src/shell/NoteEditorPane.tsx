import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";

import { deleteNote, unwrapError } from "../api";
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
  /** Called after the open note has been durably tombstoned, so the
   * caller (Shell) can drop it from its own note list state and clear
   * the selection. */
  onDeleted: (noteId: string) => Promise<void> | void;
}

/**
 * NoteEditorPane owns the title field (a plain LWW metadata register, not
 * part of the note's Yjs body - see core/model.Note) alongside the Yjs
 * body editor, and coordinates committing both together.
 */
export const NoteEditorPane = forwardRef<NoteEditorPaneHandle, NoteEditorPaneProps>(
  function NoteEditorPane({ note, onTitleCommitted, onDeleted }, ref) {
    const { t, errorMessage } = useI18n();
    const editorRef = useRef<NoteEditorHandle>(null);
    const attachmentPanelRef = useRef<AttachmentPanelHandle>(null);
    const [title, setTitle] = useState(note?.title ?? "");
    const [confirmingDelete, setConfirmingDelete] = useState(false);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);
    // Bumped after RestoreRevision commits a new current revision, and
    // included in NoteEditor's key below to force it to remount and
    // refetch the note's document: RestoreRevision writes through the
    // normal CommitNoteBody path, which the already-open Yjs document (an
    // in-memory structure with no server push) has no other way to learn
    // about.
    const [restoreVersion, setRestoreVersion] = useState(0);

    useEffect(() => {
      setTitle(note?.title ?? "");
      // Also covers a just-completed delete: onDeleted clears the parent's
      // selection, which re-renders this same component instance with
      // note=null rather than unmounting it, so `deleting` would otherwise
      // stay stuck at true from handleDelete's success path.
      setConfirmingDelete(false);
      setDeleting(false);
      setDeleteError(null);
    }, [note?.id, note?.title]);

    async function handleDelete() {
      if (!note) return;
      setDeleting(true);
      setDeleteError(null);
      try {
        await deleteNote(note.id);
        await onDeleted(note.id);
      } catch (thrown: unknown) {
        setDeleteError(errorMessage(unwrapError(thrown)));
        setDeleting(false);
      }
    }

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
        <div className="note-detail-header">
          <input
            className="note-title-input"
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            onBlur={() => void commitTitleIfChanged()}
            aria-label={t("shell.detail_title_label")}
            placeholder={t("shell.untitled_note")}
          />
          {confirmingDelete ? (
            <span className="note-delete-confirm">
              <span>{t("shell.delete_note_confirm")}</span>
              <button type="button" disabled={deleting} onClick={() => void handleDelete()}>
                {deleting ? t("shell.deleting") : t("shell.delete_confirm_button")}
              </button>
              <button
                type="button"
                className="link-button"
                disabled={deleting}
                onClick={() => setConfirmingDelete(false)}
              >
                {t("common.cancel")}
              </button>
            </span>
          ) : (
            <button type="button" className="link-button note-delete-button" onClick={() => setConfirmingDelete(true)}>
              {t("shell.delete_note")}
            </button>
          )}
        </div>
        {deleteError ? (
          <p className="error" role="alert">
            {deleteError}
          </p>
        ) : null}
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

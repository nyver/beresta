import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";

import { deleteNote, unwrapError, type SyncStatusValue } from "../api";
import { NoteEditor, type NoteEditorHandle, type NoteSaveState } from "../editor/NoteEditor";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { AttachmentPanel, type AttachmentPanelHandle } from "./AttachmentPanel";
import { KebabMenu } from "./KebabMenu";
import { Modal } from "./Modal";
import { NoteTagsEditor } from "./NoteTagsEditor";
import { RevisionsPanel } from "./RevisionsPanel";
import { SaveStatusLine } from "./SaveStatusLine";

export interface NoteEditorPaneHandle {
  /** Flushes the currently open note's pending body edit and any
   * not-yet-blurred title change. A no-op when no note is open. */
  flush: () => Promise<void>;
}

export interface NoteEditorPaneProps {
  note: main.NoteDTO | null;
  tags: main.TagDTO[];
  /** Tag ids currently assigned to the open note; ignored while no note is
   * open. */
  assignedTagIds: string[];
  /** Called with the new title right after a rename has been durably
   * committed, so the caller can patch its own note list state. Never
   * called if the commit failed - see commitTitleIfChanged. */
  onTitleCommitted: (noteId: string, title: string) => void;
  /** Called after the open note has been durably tombstoned, so the
   * caller (Shell) can drop it from its own note list state and clear
   * the selection. */
  onDeleted: (noteId: string) => Promise<void> | void;
  /** Creates a new note (in whatever notebook context Shell currently has
   * selected) - used by both the open note's kebab menu and the
   * no-note-open placeholder. */
  onCreateNote: () => void;
  /** True while a just-requested onCreateNote call is still in flight, so
   * both triggers can disable themselves against a double click. */
  creatingNote: boolean;
  /** Assigns or unassigns one tag on the open note. */
  onToggleTag: (noteId: string, tagId: string, present: boolean) => Promise<void>;
  /** Creates a new workspace tag and assigns it to the open note. */
  onCreateTag: (noteId: string, name: string) => Promise<void>;
  /** Current workspace-wide synchronization status (see Shell's own
   * "sync:status" subscription), rendered alongside the open note's local
   * save state in the footer status line below. Null before the first
   * status has loaded; defaults to null so callers that do not care about
   * synchronization (tests) need not pass it. */
  syncStatus?: SyncStatusValue | null;
  /** When syncStatus last became "current", so the status line can show a
   * static "synced at HH:MM" instead of nothing - see Shell's own doc
   * comment on why this is not a live-ticking relative time. */
  syncedAt?: number | null;
  /** Opens the Sync modal - the status line's sync fragment is clickable
   * for exactly the cases (offline/failed) where there is something to look
   * at there. */
  onOpenSync?: () => void;
}

/**
 * NoteEditorPane owns the title field (a plain LWW metadata register, not
 * part of the note's Yjs body - see core/model.Note) alongside the Yjs
 * body editor, and coordinates committing both together.
 */
export const NoteEditorPane = forwardRef<NoteEditorPaneHandle, NoteEditorPaneProps>(
  function NoteEditorPane(
    {
      note,
      tags,
      assignedTagIds,
      onTitleCommitted,
      onDeleted,
      onCreateNote,
      creatingNote,
      onToggleTag,
      onCreateTag,
      syncStatus = null,
      syncedAt = null,
      onOpenSync = () => {},
    },
    ref,
  ) {
    const { t, errorMessage } = useI18n();
    const editorRef = useRef<NoteEditorHandle>(null);
    const attachmentPanelRef = useRef<AttachmentPanelHandle>(null);
    const [title, setTitle] = useState(note?.title ?? "");
    const [confirmingDelete, setConfirmingDelete] = useState(false);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);
    const [saveState, setSaveState] = useState<NoteSaveState>({
      saving: false,
      dirty: false,
      savedAt: null,
      hasError: false,
    });
    const handleSaveStateChange = useCallback((next: NoteSaveState) => setSaveState(next), []);
    // Bumped after RestoreRevision commits a new current revision, and
    // included in NoteEditor's key below to force it to remount and
    // refetch the note's document: RestoreRevision writes through the
    // normal CommitNoteBody path, which the already-open Yjs document (an
    // in-memory structure with no server push) has no other way to learn
    // about.
    const [restoreVersion, setRestoreVersion] = useState(0);
    // History and Attachments both live behind a modal now instead of
    // sitting inline in the editor column (task: expand the editor's
    // usable area); AttachmentPanel itself stays mounted regardless (see
    // its own doc comment), but this still needs resetting on note change
    // so switching notes does not leave the previous note's modal open.
    const [historyOpen, setHistoryOpen] = useState(false);
    const [attachmentsOpen, setAttachmentsOpen] = useState(false);

    useEffect(() => {
      setTitle(note?.title ?? "");
      // Also covers a just-completed delete: onDeleted clears the parent's
      // selection, which re-renders this same component instance with
      // note=null rather than unmounting it, so `deleting` would otherwise
      // stay stuck at true from handleDelete's success path.
      setConfirmingDelete(false);
      setDeleting(false);
      setDeleteError(null);
      setHistoryOpen(false);
      setAttachmentsOpen(false);
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
          <button type="button" onClick={onCreateNote} disabled={creatingNote}>
            {t("shell.new_note_button")}
          </button>
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
            <KebabMenu
              label={t("shell.note_actions")}
              items={[
                { label: t("shell.new_note_button"), onSelect: onCreateNote, disabled: creatingNote },
                { label: t("revisions.open_button"), onSelect: () => setHistoryOpen(true) },
                { label: t("shell.delete_note"), onSelect: () => setConfirmingDelete(true), destructive: true },
              ]}
            />
          )}
        </div>
        {deleteError ? (
          <p className="error" role="alert">
            {deleteError}
          </p>
        ) : null}
        <NoteTagsEditor
          tags={tags}
          assignedTagIds={assignedTagIds}
          onToggle={(tagId, present) => onToggleTag(note.id, tagId, present)}
          onCreateAndAssign={(name) => onCreateTag(note.id, name)}
        />
        <NoteEditor
          key={`${note.id}-${restoreVersion}`}
          ref={editorRef}
          noteId={note.id}
          onAttachFiles={(files) => attachmentPanelRef.current?.attachFiles(files)}
          onSaveStateChange={handleSaveStateChange}
        />
        <div className="note-detail-footer">
          <AttachmentPanel
            key={note.id}
            ref={attachmentPanelRef}
            noteId={note.id}
            open={attachmentsOpen}
            onOpenChange={setAttachmentsOpen}
          />
          <SaveStatusLine saveState={saveState} syncStatus={syncStatus} syncedAt={syncedAt} onOpenSync={onOpenSync} />
        </div>
        {historyOpen ? (
          <Modal title={t("revisions.section_title")} onClose={() => setHistoryOpen(false)}>
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
          </Modal>
        ) : null}
      </div>
    );
  },
);

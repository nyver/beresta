import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";

import { setNoteNotebook, unwrapError, type SyncState } from "../api";
import { NoteEditor, type NoteEditorHandle, type NoteSaveState } from "../editor/NoteEditor";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { AttachmentPanel, type AttachmentPanelHandle } from "./AttachmentPanel";
import { KebabMenu } from "./KebabMenu";
import { Modal } from "./Modal";
import { buildNotebookTree, flattenVisibleNotebooks } from "./notebookTreeModel";
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
  /** Every workspace notebook, used only to populate the "Move to
   * notebook..." picker below - the keyboard/menu alternative to dragging
   * the note onto a notebook row in the sidebar (task 6.3). */
  notebooks: main.NotebookDTO[];
  /** Called after the open note has been durably refiled into a different
   * notebook, so the caller (Shell) can patch its own notes state the same
   * way it does for a drag-and-drop refile (see Shell's handleNoteMoved). */
  onMove: (noteId: string, notebookId: string) => void;
  /** Tag ids currently assigned to the open note; ignored while no note is
   * open. */
  assignedTagIds: string[];
  /** Called with the new title right after a rename has been durably
   * committed, so the caller can patch its own note list state. Never
   * called if the commit failed - see commitTitleIfChanged. */
  onTitleCommitted: (noteId: string, title: string) => void;
  /** Called when the user chooses "Delete" for the open note. Recoverable
   * ordinary note deletion (specs/notes-management) removes it from the
   * active list immediately and offers an undo, without asking for
   * confirmation here - the caller (Shell) owns that whole flow, since
   * the undo snackbar must outlive this pane once the note (and this
   * pane's selection) is gone. */
  onDelete: (noteId: string) => void;
  /** Assigns or unassigns one tag on the open note. */
  onToggleTag: (noteId: string, tagId: string, present: boolean) => Promise<void>;
  /** Creates a new workspace tag and assigns it to the open note. */
  onCreateTag: (noteId: string, name: string) => Promise<void>;
  /** The id of a note that was just created and should receive focus once
   * (see onAutoFocusConsumed), or "" for any note opened by ordinary
   * selection. */
  autoFocusNoteId?: string;
  /** Called immediately after autoFocusNoteId has been acted on, so the
   * caller can clear it back to "" - autofocus must fire only once per
   * created note, not again if the user navigates away and back to it. */
  onAutoFocusConsumed?: () => void;
  /** Called the first time the open note's title, body, tags, or
   * attachments are edited - only meaningful while the open note is the
   * caller's tracked untouched draft (see Shell's draftNoteIdRef); a
   * no-op call for any other note is harmless. */
  onDraftTouched?: () => void;
  /** Current workspace-wide synchronization state (see Shell's own
   * "sync:summary" subscription), rendered alongside the open note's local
   * save state in the footer status line below. Null before the first
   * summary has loaded; defaults to null so callers that do not care about
   * synchronization (tests) need not pass it. */
  syncStatus?: SyncState | null;
  /** When syncStatus last became "current", so the status line can show a
   * static "synced at HH:MM" instead of nothing - see Shell's own doc
   * comment on why this is not a live-ticking relative time. */
  syncedAt?: number | null;
  /** Opens the Sync modal - the status line's sync fragment is clickable
   * for exactly the cases (offline/retrying/action_required) where there
   * is something to look at there. */
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
      notebooks,
      onMove,
      assignedTagIds,
      onTitleCommitted,
      onDelete,
      onToggleTag,
      onCreateTag,
      autoFocusNoteId = "",
      onAutoFocusConsumed,
      onDraftTouched,
      syncStatus = null,
      syncedAt = null,
      onOpenSync = () => {},
    },
    ref,
  ) {
    const { t, errorMessage } = useI18n();
    const editorRef = useRef<NoteEditorHandle>(null);
    const attachmentPanelRef = useRef<AttachmentPanelHandle>(null);
    const titleInputRef = useRef<HTMLInputElement>(null);
    const [title, setTitle] = useState(note?.title ?? "");
    const [saveState, setSaveState] = useState<NoteSaveState>({ state: null });
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
    // The keyboard/menu alternative to dragging this note onto a notebook
    // row in the sidebar (task 6.3): same setNoteNotebook call, same
    // localized error surfaced through formError.
    const [moveOpen, setMoveOpen] = useState(false);
    const [moveBusy, setMoveBusy] = useState(false);
    const [moveError, setMoveError] = useState<string | null>(null);
    const notebookNodes = useMemo(
      () => flattenVisibleNotebooks(buildNotebookTree(notebooks), new Set(notebooks.map((n) => n.id))),
      [notebooks],
    );

    useEffect(() => {
      setTitle(note?.title ?? "");
      // Also covers a just-completed delete: onDelete clears the parent's
      // selection, which re-renders this same component instance with
      // note=null rather than unmounting it.
      setHistoryOpen(false);
      setAttachmentsOpen(false);
      setMoveOpen(false);
      setMoveError(null);
    }, [note?.id, note?.title]);

    async function handleMoveTo(notebookId: string) {
      if (!note || moveBusy) return;
      setMoveBusy(true);
      setMoveError(null);
      try {
        await setNoteNotebook(note.id, notebookId);
        onMove(note.id, notebookId);
        setMoveOpen(false);
      } catch (thrown: unknown) {
        setMoveError(errorMessage(unwrapError(thrown)));
      } finally {
        setMoveBusy(false);
      }
    }

    // Instant creation and durable automatic save (specs/notes-management):
    // a newly created note SHALL be focused without a modal. Only fires for
    // the specific note Shell just created (autoFocusNoteId), never when
    // the user merely selects an existing note from the list.
    useEffect(() => {
      if (note?.id && note.id === autoFocusNoteId) {
        titleInputRef.current?.focus();
        onAutoFocusConsumed?.();
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [note?.id, autoFocusNoteId]);

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
      return <div className="note-detail note-detail-empty" />;
    }

    return (
      <div className="note-detail">
        <div className="note-detail-header">
          <input
            ref={titleInputRef}
            className="note-title-input"
            value={title}
            onChange={(event) => {
              setTitle(event.target.value);
              onDraftTouched?.();
            }}
            onBlur={() => void commitTitleIfChanged()}
            aria-label={t("shell.detail_title_label")}
            placeholder={t("shell.untitled_note")}
          />
          <KebabMenu
            label={t("shell.note_actions")}
            items={[
              { label: t("revisions.open_button"), onSelect: () => setHistoryOpen(true) },
              {
                label: t("shell.move_to_notebook"),
                onSelect: () => {
                  setMoveError(null);
                  setMoveOpen(true);
                },
              },
              { label: t("shell.delete_note"), onSelect: () => onDelete(note.id), destructive: true },
            ]}
          />
        </div>
        {moveOpen ? (
          <Modal title={t("shell.move_to_notebook_title")} onClose={() => (moveBusy ? undefined : setMoveOpen(false))}>
            <ul className="notebook-move-list">
              <li>
                <button
                  type="button"
                  disabled={moveBusy || note.notebook_id === ""}
                  onClick={() => void handleMoveTo("")}
                >
                  {t("shell.no_notebook")}
                </button>
              </li>
              {notebookNodes.map((node) => (
                <li key={node.notebook.id} style={{ paddingLeft: `${node.depth}rem` }}>
                  <button
                    type="button"
                    disabled={moveBusy || note.notebook_id === node.notebook.id}
                    onClick={() => void handleMoveTo(node.notebook.id)}
                  >
                    {node.notebook.name}
                  </button>
                </li>
              ))}
            </ul>
            {moveBusy ? <p>{t("shell.moving")}</p> : null}
            {moveError ? (
              <p className="error" role="alert">
                {moveError}
              </p>
            ) : null}
          </Modal>
        ) : null}
        <NoteTagsEditor
          tags={tags}
          assignedTagIds={assignedTagIds}
          onToggle={(tagId, present) => {
            onDraftTouched?.();
            return onToggleTag(note.id, tagId, present);
          }}
          onCreateAndAssign={(name) => {
            onDraftTouched?.();
            return onCreateTag(note.id, name);
          }}
        />
        <NoteEditor
          key={`${note.id}-${restoreVersion}`}
          ref={editorRef}
          noteId={note.id}
          onAttachFiles={(files) => {
            onDraftTouched?.();
            attachmentPanelRef.current?.attachFiles(files);
          }}
          onSaveStateChange={handleSaveStateChange}
          onBodyTouched={onDraftTouched}
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

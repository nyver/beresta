import { useMemo, useState, type DragEvent } from "react";

import { createNotebook, deleteNotebook, moveNotebook, renameNotebook, setNoteNotebook, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { KebabMenu } from "./KebabMenu";
import { Modal } from "./Modal";
import { buildNotebookTree, flattenVisibleNotebooks, type NotebookNode } from "./notebookTreeModel";

// Custom drag payload types (see NoteList.tsx for the note-side source):
// distinguishing them lets a single drop target (a notebook row) tell a
// dragged notebook (reparent) apart from a dragged note (refile).
const DRAG_TYPE_NOTEBOOK = "application/x-beresta-notebook-id";
const DRAG_TYPE_NOTE = "application/x-beresta-note-id";

// The single modal that can be open at a time: naming a new notebook,
// renaming an existing one, or confirming a deletion. Modeling these as one
// discriminated union (rather than three separate open/id state pairs)
// makes "only one dialog open at once" structural instead of a convention
// callers have to maintain.
type NotebookDialog =
  | { kind: "create"; parentId: string }
  | { kind: "rename"; notebookId: string }
  | { kind: "delete"; notebookId: string; name: string };

export interface NotebookTreeProps {
  notebooks: main.NotebookDTO[];
  /** "" selects the synthetic "All Notes" root; null means neither it nor
   * any notebook is the active selection (a tag is selected instead). */
  selectedId: string | null;
  onSelect: (notebookId: string) => void;
  /** Creates a note directly in the notebook selected from its row menu. */
  onCreateNote: (notebookId: string) => void;
  /** Called after a new notebook has been durably created, so the caller
   * (Shell) can add it to its own notebooks state. */
  onCreated: (notebook: main.NotebookDTO) => void;
  /** Called after a notebook has been durably renamed, so the caller
   * (Shell) can patch its own notebooks state's name. */
  onRenamed: (notebookId: string, name: string) => void;
  /** Called after a notebook has been durably tombstoned, so the caller
   * (Shell) can drop it from its own notebooks state. */
  onDeleted: (notebookId: string) => void;
  /** Called after a notebook has been durably reparented (drag-and-drop),
   * so the caller can patch its own notebooks state's parent_id. */
  onMoved: (notebookId: string, newParentId: string) => void;
  /** Called after a note has been durably refiled into a different
   * notebook (dragged from the note list), so the caller can patch its own
   * notes state's notebook_id. */
  onNoteMoved: (noteId: string, notebookId: string) => void;
}

/**
 * NotebookTree renders notebooks as plain nested buttons (Tab order plus
 * native Enter/Space activation) rather than a full WAI-ARIA treeview
 * widget (roving tabindex, arrow-key focus movement): for a sidebar of
 * this size, native interactive elements give the same real keyboard
 * accessibility with far less custom event-handling code to get right.
 */
export function NotebookTree({
  notebooks,
  selectedId,
  onSelect,
  onCreateNote,
  onCreated,
  onRenamed,
  onDeleted,
  onMoved,
  onNoteMoved,
}: NotebookTreeProps) {
  const { t, errorMessage } = useI18n();
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [dialog, setDialog] = useState<NotebookDialog | null>(null);
  const [formName, setFormName] = useState("");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  // Id of the row currently under a valid drag-over (for the highlight
  // outline); "" is the "All Notes" root drop target.
  const [dragOverId, setDragOverId] = useState<string | null>(null);
  const [moveError, setMoveError] = useState<string | null>(null);

  const tree = useMemo(() => buildNotebookTree(notebooks), [notebooks]);
  const visible = useMemo(() => flattenVisibleNotebooks(tree, expanded), [tree, expanded]);

  function toggle(id: string) {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  function startCreate(parentId: string) {
    setFormError(null);
    setFormName("");
    setDialog({ kind: "create", parentId });
  }

  function startRename(notebook: main.NotebookDTO) {
    setFormError(null);
    setFormName(notebook.name);
    setDialog({ kind: "rename", notebookId: notebook.id });
  }

  function startDelete(notebook: main.NotebookDTO) {
    setFormError(null);
    setDialog({ kind: "delete", notebookId: notebook.id, name: notebook.name });
  }

  function closeDialog() {
    if (busy) return;
    setDialog(null);
  }

  async function handleCreateSubmit() {
    const name = formName.trim();
    if (!name || busy || dialog?.kind !== "create") return;
    setBusy(true);
    setFormError(null);
    try {
      const notebook = await createNotebook(dialog.parentId, name);
      onCreated(notebook);
      setDialog(null);
    } catch (thrown: unknown) {
      setFormError(errorMessage(unwrapError(thrown)));
    } finally {
      setBusy(false);
    }
  }

  async function handleRenameSubmit() {
    const name = formName.trim();
    if (!name || busy || dialog?.kind !== "rename") return;
    setBusy(true);
    setFormError(null);
    try {
      await renameNotebook(dialog.notebookId, name);
      onRenamed(dialog.notebookId, name);
      setDialog(null);
    } catch (thrown: unknown) {
      setFormError(errorMessage(unwrapError(thrown)));
    } finally {
      setBusy(false);
    }
  }

  async function handleDeleteConfirm() {
    if (busy || dialog?.kind !== "delete") return;
    setBusy(true);
    setFormError(null);
    try {
      await deleteNotebook(dialog.notebookId);
      onDeleted(dialog.notebookId);
      setDialog(null);
    } catch (thrown: unknown) {
      setFormError(errorMessage(unwrapError(thrown)));
    } finally {
      setBusy(false);
    }
  }

  function acceptsDrag(event: DragEvent) {
    return event.dataTransfer.types.includes(DRAG_TYPE_NOTEBOOK) || event.dataTransfer.types.includes(DRAG_TYPE_NOTE);
  }

  function handleDragOver(rowId: string, event: DragEvent) {
    if (!acceptsDrag(event)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    if (dragOverId !== rowId) setDragOverId(rowId);
  }

  function handleDragLeave(rowId: string) {
    setDragOverId((current) => (current === rowId ? null : current));
  }

  async function handleDrop(targetNotebookId: string, event: DragEvent) {
    event.preventDefault();
    setDragOverId(null);
    const draggedNotebookId = event.dataTransfer.getData(DRAG_TYPE_NOTEBOOK);
    const draggedNoteId = event.dataTransfer.getData(DRAG_TYPE_NOTE);
    setMoveError(null);
    try {
      if (draggedNotebookId) {
        if (draggedNotebookId === targetNotebookId) return;
        await moveNotebook(draggedNotebookId, targetNotebookId);
        onMoved(draggedNotebookId, targetNotebookId);
      } else if (draggedNoteId) {
        await setNoteNotebook(draggedNoteId, targetNotebookId);
        onNoteMoved(draggedNoteId, targetNotebookId);
      }
    } catch (thrown: unknown) {
      setMoveError(errorMessage(unwrapError(thrown)));
    }
  }

  return (
    <nav aria-label={t("shell.notebooks_section")} className="notebook-tree">
      <div className="tree-section-header">
        <h2>{t("shell.notebooks_section")}</h2>
        <KebabMenu
          label={t("shell.notebooks_section_actions")}
          items={[
            { label: t("shell.new_note_button"), onSelect: () => onCreateNote("") },
            { label: t("shell.new_notebook_menu_item"), onSelect: () => startCreate("") },
          ]}
        />
      </div>
      <button
        type="button"
        className={`tree-row${selectedId === "" ? " selected" : ""}${dragOverId === "" ? " drag-over" : ""}`}
        onClick={() => onSelect("")}
        onDragOver={(event) => handleDragOver("", event)}
        onDragLeave={() => handleDragLeave("")}
        onDrop={(event) => void handleDrop("", event)}
      >
        {t("shell.all_notes")}
      </button>
      <ul>
        {visible.map((node) => (
          <li key={node.notebook.id} style={{ paddingLeft: `${node.depth}rem` }}>
            <NotebookRow
              node={node}
              expanded={expanded.has(node.notebook.id)}
              selected={node.notebook.id === selectedId}
              dragOver={dragOverId === node.notebook.id}
              onToggle={() => toggle(node.notebook.id)}
              onSelect={() => onSelect(node.notebook.id)}
              onCreateNote={() => onCreateNote(node.notebook.id)}
              onRequestCreateChild={() => startCreate(node.notebook.id)}
              onRequestRename={() => startRename(node.notebook)}
              onRequestDelete={() => startDelete(node.notebook)}
              onDragOver={(event) => handleDragOver(node.notebook.id, event)}
              onDragLeave={() => handleDragLeave(node.notebook.id)}
              onDrop={(event) => void handleDrop(node.notebook.id, event)}
            />
          </li>
        ))}
      </ul>
      {moveError ? (
        <p className="error" role="alert">
          {moveError}
        </p>
      ) : null}
      {dialog?.kind === "create" || dialog?.kind === "rename" ? (
        <Modal
          title={dialog.kind === "create" ? t("shell.new_notebook_menu_item") : t("shell.rename_notebook")}
          onClose={closeDialog}
        >
          <form
            className="notebook-create"
            onSubmit={(event) => {
              event.preventDefault();
              void (dialog.kind === "create" ? handleCreateSubmit() : handleRenameSubmit());
            }}
          >
            <input
              type="text"
              autoFocus
              value={formName}
              onChange={(event) => setFormName(event.target.value)}
              placeholder={t("shell.new_notebook_placeholder")}
              aria-label={t("shell.new_notebook_placeholder")}
            />
            <button type="submit" disabled={busy || !formName.trim()}>
              {dialog.kind === "create" ? t("shell.new_notebook_button") : t("shell.rename_notebook_button")}
            </button>
            <button type="button" className="link-button" onClick={closeDialog} disabled={busy}>
              {t("common.cancel")}
            </button>
          </form>
          {formError ? (
            <p className="error" role="alert">
              {formError}
            </p>
          ) : null}
        </Modal>
      ) : null}
      {dialog?.kind === "delete" ? (
        <Modal title={`${t("shell.delete_notebook")}: ${dialog.name}`} onClose={closeDialog}>
          <p>{t("shell.delete_notebook_confirm")}</p>
          <div className="dialog-actions">
            <button type="button" disabled={busy} onClick={() => void handleDeleteConfirm()}>
              {busy ? t("shell.deleting") : t("shell.delete_confirm_button")}
            </button>
            <button type="button" className="link-button" disabled={busy} onClick={closeDialog}>
              {t("common.cancel")}
            </button>
          </div>
          {formError ? (
            <p className="error" role="alert">
              {formError}
            </p>
          ) : null}
        </Modal>
      ) : null}
    </nav>
  );
}

function NotebookRow({
  node,
  expanded,
  selected,
  dragOver,
  onToggle,
  onSelect,
  onCreateNote,
  onRequestCreateChild,
  onRequestRename,
  onRequestDelete,
  onDragOver,
  onDragLeave,
  onDrop,
}: {
  node: NotebookNode;
  expanded: boolean;
  selected: boolean;
  dragOver: boolean;
  onToggle: () => void;
  onSelect: () => void;
  onCreateNote: () => void;
  onRequestCreateChild: () => void;
  onRequestRename: () => void;
  onRequestDelete: () => void;
  onDragOver: (event: DragEvent) => void;
  onDragLeave: () => void;
  onDrop: (event: DragEvent) => void;
}) {
  const { t } = useI18n();
  const hasChildren = node.children.length > 0;

  return (
    <div
      className={`notebook-row-container${dragOver ? " drag-over" : ""}`}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
    >
      {hasChildren ? (
        <button
          type="button"
          className="tree-toggle"
          aria-label={expanded ? t("shell.collapse_notebook") : t("shell.expand_notebook")}
          onClick={onToggle}
        >
          {expanded ? "▾" : "▸"}
        </button>
      ) : (
        <span className="tree-toggle-spacer" aria-hidden="true" />
      )}
      <button
        type="button"
        draggable
        className={`tree-row${selected ? " selected" : ""}`}
        onClick={onSelect}
        onDragStart={(event) => {
          event.dataTransfer.setData(DRAG_TYPE_NOTEBOOK, node.notebook.id);
          event.dataTransfer.effectAllowed = "move";
        }}
      >
        {node.notebook.name}
      </button>
      <KebabMenu
        label={`${t("shell.notebook_actions")}: ${node.notebook.name}`}
        items={[
          { label: t("shell.new_note_button"), onSelect: onCreateNote },
          { label: t("shell.new_notebook_menu_item"), onSelect: onRequestCreateChild },
          { label: t("shell.rename_notebook"), onSelect: onRequestRename },
          { label: t("shell.delete_notebook"), onSelect: onRequestDelete, destructive: true },
        ]}
      />
    </div>
  );
}

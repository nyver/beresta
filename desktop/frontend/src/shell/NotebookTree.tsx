import { useMemo, useState, type DragEvent } from "react";

import { createNotebook, deleteNotebook, moveNotebook, setNoteNotebook, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { KebabMenu } from "./KebabMenu";
import { buildNotebookTree, flattenVisibleNotebooks, type NotebookNode } from "./notebookTreeModel";

// Custom drag payload types (see NoteList.tsx for the note-side source):
// distinguishing them lets a single drop target (a notebook row) tell a
// dragged notebook (reparent) apart from a dragged note (refile).
const DRAG_TYPE_NOTEBOOK = "application/x-beresta-notebook-id";
const DRAG_TYPE_NOTE = "application/x-beresta-note-id";

export interface NotebookTreeProps {
  notebooks: main.NotebookDTO[];
  /** "" selects the synthetic "All Notes" root; null means neither it nor
   * any notebook is the active selection (a tag is selected instead). */
  selectedId: string | null;
  onSelect: (notebookId: string) => void;
  /** Called after a new notebook has been durably created, so the caller
   * (Shell) can add it to its own notebooks state. */
  onCreated: (notebook: main.NotebookDTO) => void;
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
  onCreated,
  onDeleted,
  onMoved,
  onNoteMoved,
}: NotebookTreeProps) {
  const { t, errorMessage } = useI18n();
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  // Which row's "create a notebook here" inline form is open: "" means the
  // root-level form (triggered from the section header's kebab), a
  // notebook id means that row's "new subnotebook" form, null means none
  // open. Only one at a time, mirroring confirmingDeleteId below.
  const [creatingParentId, setCreatingParentId] = useState<string | null>(null);
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  // Only one row's delete confirmation is shown at a time, tracked by
  // notebook id rather than a plain boolean so switching the confirm
  // target (clicking a different row's delete button) just moves it.
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
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
    setCreateError(null);
    setNewName("");
    setCreatingParentId(parentId);
  }

  async function handleCreate() {
    const name = newName.trim();
    if (!name || creating || creatingParentId === null) return;
    setCreating(true);
    setCreateError(null);
    try {
      const notebook = await createNotebook(creatingParentId, name);
      onCreated(notebook);
      setNewName("");
      setCreatingParentId(null);
    } catch (thrown: unknown) {
      setCreateError(errorMessage(unwrapError(thrown)));
    } finally {
      setCreating(false);
    }
  }

  async function handleDelete(id: string) {
    setDeleting(true);
    setDeleteError(null);
    try {
      await deleteNotebook(id);
      onDeleted(id);
      setConfirmingDeleteId(null);
    } catch (thrown: unknown) {
      setDeleteError(errorMessage(unwrapError(thrown)));
    } finally {
      setDeleting(false);
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
          items={[{ label: t("shell.new_notebook_menu_item"), onSelect: () => startCreate("") }]}
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
      {creatingParentId === "" ? (
        <NotebookCreateForm
          name={newName}
          creating={creating}
          onChange={setNewName}
          onSubmit={() => void handleCreate()}
          onCancel={() => setCreatingParentId(null)}
        />
      ) : null}
      {createError && creatingParentId === "" ? (
        <p className="error" role="alert">
          {createError}
        </p>
      ) : null}
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
              confirmingDelete={confirmingDeleteId === node.notebook.id}
              deleting={deleting}
              onRequestCreateChild={() => startCreate(node.notebook.id)}
              onRequestDelete={() => {
                setDeleteError(null);
                setConfirmingDeleteId(node.notebook.id);
              }}
              onCancelDelete={() => setConfirmingDeleteId(null)}
              onConfirmDelete={() => void handleDelete(node.notebook.id)}
              onDragOver={(event) => handleDragOver(node.notebook.id, event)}
              onDragLeave={() => handleDragLeave(node.notebook.id)}
              onDrop={(event) => void handleDrop(node.notebook.id, event)}
            />
            {creatingParentId === node.notebook.id ? (
              <NotebookCreateForm
                name={newName}
                creating={creating}
                onChange={setNewName}
                onSubmit={() => void handleCreate()}
                onCancel={() => setCreatingParentId(null)}
              />
            ) : null}
            {createError && creatingParentId === node.notebook.id ? (
              <p className="error" role="alert">
                {createError}
              </p>
            ) : null}
          </li>
        ))}
      </ul>
      {deleteError ? (
        <p className="error" role="alert">
          {deleteError}
        </p>
      ) : null}
      {moveError ? (
        <p className="error" role="alert">
          {moveError}
        </p>
      ) : null}
    </nav>
  );
}

function NotebookCreateForm({
  name,
  creating,
  onChange,
  onSubmit,
  onCancel,
}: {
  name: string;
  creating: boolean;
  onChange: (value: string) => void;
  onSubmit: () => void;
  onCancel: () => void;
}) {
  const { t } = useI18n();
  return (
    <form
      className="notebook-create"
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <input
        type="text"
        autoFocus
        value={name}
        onChange={(event) => onChange(event.target.value)}
        placeholder={t("shell.new_notebook_placeholder")}
        aria-label={t("shell.new_notebook_placeholder")}
      />
      <button type="submit" disabled={creating || !name.trim()}>
        {t("shell.new_notebook_button")}
      </button>
      <button type="button" className="link-button" onClick={onCancel} disabled={creating}>
        {t("common.cancel")}
      </button>
    </form>
  );
}

function NotebookRow({
  node,
  expanded,
  selected,
  dragOver,
  onToggle,
  onSelect,
  confirmingDelete,
  deleting,
  onRequestCreateChild,
  onRequestDelete,
  onCancelDelete,
  onConfirmDelete,
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
  confirmingDelete: boolean;
  deleting: boolean;
  onRequestCreateChild: () => void;
  onRequestDelete: () => void;
  onCancelDelete: () => void;
  onConfirmDelete: () => void;
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
      {confirmingDelete ? (
        <span className="tree-row-delete-confirm">
          <button type="button" disabled={deleting} onClick={onConfirmDelete}>
            {deleting ? t("shell.deleting") : t("shell.delete_confirm_button")}
          </button>
          <button type="button" className="link-button" disabled={deleting} onClick={onCancelDelete}>
            {t("common.cancel")}
          </button>
        </span>
      ) : (
        <KebabMenu
          label={`${t("shell.notebook_actions")}: ${node.notebook.name}`}
          items={[
            { label: t("shell.new_notebook_menu_item"), onSelect: onRequestCreateChild },
            { label: t("shell.delete_notebook"), onSelect: onRequestDelete, destructive: true },
          ]}
        />
      )}
    </div>
  );
}

import { useMemo, useState } from "react";

import { createNotebook, deleteNotebook, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { buildNotebookTree, flattenVisibleNotebooks, type NotebookNode } from "./notebookTreeModel";

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
}

/**
 * NotebookTree renders notebooks as plain nested buttons (Tab order plus
 * native Enter/Space activation) rather than a full WAI-ARIA treeview
 * widget (roving tabindex, arrow-key focus movement): for a sidebar of
 * this size, native interactive elements give the same real keyboard
 * accessibility with far less custom event-handling code to get right.
 */
export function NotebookTree({ notebooks, selectedId, onSelect, onCreated, onDeleted }: NotebookTreeProps) {
  const { t, errorMessage } = useI18n();
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  // Only one row's delete confirmation is shown at a time, tracked by
  // notebook id rather than a plain boolean so switching the confirm
  // target (clicking a different row's delete button) just moves it.
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

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

  async function handleCreate() {
    const name = newName.trim();
    if (!name || creating) return;
    setCreating(true);
    setCreateError(null);
    try {
      const notebook = await createNotebook("", name);
      onCreated(notebook);
      setNewName("");
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

  return (
    <nav aria-label={t("shell.notebooks_section")} className="notebook-tree">
      <h2>{t("shell.notebooks_section")}</h2>
      <button
        type="button"
        className={`tree-row${selectedId === "" ? " selected" : ""}`}
        onClick={() => onSelect("")}
      >
        {t("shell.all_notes")}
      </button>
      <ul>
        {visible.map((node) => (
          <NotebookRow
            key={node.notebook.id}
            node={node}
            expanded={expanded.has(node.notebook.id)}
            selected={node.notebook.id === selectedId}
            onToggle={() => toggle(node.notebook.id)}
            onSelect={() => onSelect(node.notebook.id)}
            confirmingDelete={confirmingDeleteId === node.notebook.id}
            deleting={deleting}
            onRequestDelete={() => {
              setDeleteError(null);
              setConfirmingDeleteId(node.notebook.id);
            }}
            onCancelDelete={() => setConfirmingDeleteId(null)}
            onConfirmDelete={() => void handleDelete(node.notebook.id)}
          />
        ))}
      </ul>
      {deleteError ? (
        <p className="error" role="alert">
          {deleteError}
        </p>
      ) : null}
      <form
        className="notebook-create"
        onSubmit={(event) => {
          event.preventDefault();
          void handleCreate();
        }}
      >
        <input
          type="text"
          value={newName}
          onChange={(event) => setNewName(event.target.value)}
          placeholder={t("shell.new_notebook_placeholder")}
          aria-label={t("shell.new_notebook_placeholder")}
        />
        <button type="submit" disabled={creating || !newName.trim()}>
          {t("shell.new_notebook_button")}
        </button>
      </form>
      {createError ? (
        <p className="error" role="alert">
          {createError}
        </p>
      ) : null}
    </nav>
  );
}

function NotebookRow({
  node,
  expanded,
  selected,
  onToggle,
  onSelect,
  confirmingDelete,
  deleting,
  onRequestDelete,
  onCancelDelete,
  onConfirmDelete,
}: {
  node: NotebookNode;
  expanded: boolean;
  selected: boolean;
  onToggle: () => void;
  onSelect: () => void;
  confirmingDelete: boolean;
  deleting: boolean;
  onRequestDelete: () => void;
  onCancelDelete: () => void;
  onConfirmDelete: () => void;
}) {
  const { t } = useI18n();
  const hasChildren = node.children.length > 0;

  return (
    <li style={{ paddingLeft: `${node.depth}rem` }}>
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
        className={`tree-row${selected ? " selected" : ""}`}
        onClick={onSelect}
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
        <button
          type="button"
          className="tree-row-delete"
          aria-label={t("shell.delete_notebook")}
          onClick={onRequestDelete}
        >
          ×
        </button>
      )}
    </li>
  );
}

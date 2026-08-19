import { useMemo, useState } from "react";

import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { buildNotebookTree, flattenVisibleNotebooks, type NotebookNode } from "./notebookTreeModel";

export interface NotebookTreeProps {
  notebooks: main.NotebookDTO[];
  /** "" selects the synthetic "All Notes" root; null means neither it nor
   * any notebook is the active selection (a tag is selected instead). */
  selectedId: string | null;
  onSelect: (notebookId: string) => void;
}

/**
 * NotebookTree renders notebooks as plain nested buttons (Tab order plus
 * native Enter/Space activation) rather than a full WAI-ARIA treeview
 * widget (roving tabindex, arrow-key focus movement): for a sidebar of
 * this size, native interactive elements give the same real keyboard
 * accessibility with far less custom event-handling code to get right.
 */
export function NotebookTree({ notebooks, selectedId, onSelect }: NotebookTreeProps) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());

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
          />
        ))}
      </ul>
    </nav>
  );
}

function NotebookRow({
  node,
  expanded,
  selected,
  onToggle,
  onSelect,
}: {
  node: NotebookNode;
  expanded: boolean;
  selected: boolean;
  onToggle: () => void;
  onSelect: () => void;
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
    </li>
  );
}

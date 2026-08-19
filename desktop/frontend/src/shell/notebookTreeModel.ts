import { main } from "../../wailsjs/go/models";

export interface NotebookNode {
  notebook: main.NotebookDTO;
  depth: number;
  children: NotebookNode[];
}

/**
 * buildNotebookTree arranges a flat NotebookDTO list (as returned by
 * ListNotebooks, parent-linked via parent_id, "" meaning workspace root)
 * into a tree, dropping tombstoned notebooks from navigation and sorting
 * siblings by name. It does not detect cycles: the backend's own
 * MoveNotebook already rejects any mutation that would create one (see
 * store.ErrNotebookCycle), so a cycle here would only occur if the
 * dataset itself were corrupt, which is out of scope for a display
 * helper to defend against.
 */
export function buildNotebookTree(notebooks: main.NotebookDTO[]): NotebookNode[] {
  const byParent = new Map<string, main.NotebookDTO[]>();
  for (const notebook of notebooks) {
    if (notebook.deleted) continue;
    const siblings = byParent.get(notebook.parent_id) ?? [];
    siblings.push(notebook);
    byParent.set(notebook.parent_id, siblings);
  }
  for (const siblings of byParent.values()) {
    siblings.sort((a, b) => a.name.localeCompare(b.name));
  }

  function build(parentId: string, depth: number): NotebookNode[] {
    return (byParent.get(parentId) ?? []).map((notebook) => ({
      notebook,
      depth,
      children: build(notebook.id, depth + 1),
    }));
  }
  return build("", 0);
}

/**
 * flattenVisibleNotebooks walks the tree in display order, descending
 * into a node's children only when its id is present in expandedIds -
 * the set of currently expanded notebooks (collapsed is the default for
 * any id not in the set, so a fresh tree starts fully collapsed below
 * the roots).
 */
export function flattenVisibleNotebooks(
  nodes: NotebookNode[],
  expandedIds: ReadonlySet<string>,
): NotebookNode[] {
  const visible: NotebookNode[] = [];
  function walk(level: NotebookNode[]) {
    for (const node of level) {
      visible.push(node);
      if (node.children.length > 0 && expandedIds.has(node.notebook.id)) {
        walk(node.children);
      }
    }
  }
  walk(nodes);
  return visible;
}

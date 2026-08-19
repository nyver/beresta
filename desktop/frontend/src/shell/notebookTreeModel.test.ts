import { describe, expect, it } from "vitest";

import { main } from "../../wailsjs/go/models";
import { buildNotebookTree, flattenVisibleNotebooks } from "./notebookTreeModel";

function notebook(overrides: Partial<main.NotebookDTO>): main.NotebookDTO {
  return {
    id: "id",
    workspace_id: "ws",
    parent_id: "",
    name: "Notebook",
    deleted: false,
    ...overrides,
  };
}

describe("buildNotebookTree", () => {
  it("nests children under their parent and sorts siblings by name", () => {
    const tree = buildNotebookTree([
      notebook({ id: "b", parent_id: "", name: "Bravo" }),
      notebook({ id: "a", parent_id: "", name: "Alpha" }),
      notebook({ id: "a1", parent_id: "a", name: "Alpha child" }),
    ]);

    expect(tree.map((n) => n.notebook.id)).toEqual(["a", "b"]);
    expect(tree[0].children.map((n) => n.notebook.id)).toEqual(["a1"]);
    expect(tree[0].children[0].depth).toBe(1);
    expect(tree[1].children).toEqual([]);
  });

  it("excludes tombstoned notebooks from the tree", () => {
    const tree = buildNotebookTree([
      notebook({ id: "a", name: "Alpha", deleted: true }),
      notebook({ id: "b", name: "Bravo" }),
    ]);

    expect(tree.map((n) => n.notebook.id)).toEqual(["b"]);
  });
});

describe("flattenVisibleNotebooks", () => {
  it("hides descendants of a collapsed node and reveals them once expanded", () => {
    const tree = buildNotebookTree([
      notebook({ id: "a", name: "Alpha" }),
      notebook({ id: "a1", parent_id: "a", name: "Child" }),
    ]);

    expect(flattenVisibleNotebooks(tree, new Set()).map((n) => n.notebook.id)).toEqual(["a"]);
    expect(flattenVisibleNotebooks(tree, new Set(["a"])).map((n) => n.notebook.id)).toEqual([
      "a",
      "a1",
    ]);
  });
});

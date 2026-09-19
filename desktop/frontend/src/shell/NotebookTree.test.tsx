import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { appMock } from "../setupTests";
import { I18nProvider } from "../i18n";
import { fakeNotebook, mockLocaleCatalog, mockSettings } from "../testUtils";
import { NotebookTree } from "./NotebookTree";

function renderTree(notebooks: ReturnType<typeof fakeNotebook>[], selectedId: string | null = "") {
  mockLocaleCatalog();
  mockSettings();
  const onSelect = vi.fn();
  const onCreateNote = vi.fn();
  const onCreated = vi.fn();
  const onRenamed = vi.fn();
  const onDeleted = vi.fn();
  const onMoved = vi.fn();
  const onNoteMoved = vi.fn();
  render(
    <I18nProvider>
      <NotebookTree
        notebooks={notebooks}
        selectedId={selectedId}
        onSelect={onSelect}
        onCreateNote={onCreateNote}
        onCreated={onCreated}
        onRenamed={onRenamed}
        onDeleted={onDeleted}
        onMoved={onMoved}
        onNoteMoved={onNoteMoved}
      />
    </I18nProvider>,
  );
  return { onSelect, onCreateNote, onCreated, onRenamed, onDeleted, onMoved, onNoteMoved };
}

describe("NotebookTree", () => {
  it("always shows the All Notes root and selects it by default", async () => {
    renderTree([]);
    const allNotes = await screen.findByRole("button", { name: "shell.all_notes" });
    expect(allNotes).toHaveClass("selected");
  });

  it("selecting a notebook reports its id", async () => {
    const root = fakeNotebook({ name: "Work" });
    const { onSelect } = renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Work" }));
    expect(onSelect).toHaveBeenCalledWith(root.id);
  });

  it("hides a child notebook until its parent is expanded", async () => {
    const root = fakeNotebook({ name: "Work" });
    const child = fakeNotebook({ name: "Projects", parent_id: root.id });
    renderTree([root, child]);
    const user = userEvent.setup();

    expect(screen.queryByRole("button", { name: "Projects" })).not.toBeInTheDocument();

    await user.click(await screen.findByRole("button", { name: "shell.expand_notebook" }));
    expect(await screen.findByRole("button", { name: "Projects" })).toBeInTheDocument();
  });

  it("excludes tombstoned notebooks", () => {
    renderTree([fakeNotebook({ name: "Deleted", deleted: true })]);
    expect(screen.queryByRole("button", { name: "Deleted" })).not.toBeInTheDocument();
  });

  it("creates a new root notebook from the section menu", async () => {
    const created = fakeNotebook({ name: "Personal" });
    appMock.CreateNotebook.mockResolvedValue(created);
    const { onCreated } = renderTree([]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebooks_section_actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.new_notebook_menu_item" }));
    await user.type(
      await screen.findByRole("textbox", { name: "shell.new_notebook_placeholder" }),
      "Personal",
    );
    await user.click(await screen.findByRole("button", { name: "shell.new_notebook_button" }));

    expect(appMock.CreateNotebook).toHaveBeenCalledWith("", "Personal");
    expect(onCreated).toHaveBeenCalledWith(created);
  });

  it("creates a note in the workspace root from the section menu", async () => {
    const { onCreateNote } = renderTree([]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebooks_section_actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.new_note_button" }));

    expect(onCreateNote).toHaveBeenCalledWith("");
  });

  it("shows an error when notebook creation fails", async () => {
    appMock.CreateNotebook.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    renderTree([]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebooks_section_actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.new_notebook_menu_item" }));
    await user.type(
      await screen.findByRole("textbox", { name: "shell.new_notebook_placeholder" }),
      "Personal",
    );
    await user.click(await screen.findByRole("button", { name: "shell.new_notebook_button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
  });

  it("creates a child notebook from a row's menu", async () => {
    const root = fakeNotebook({ name: "Work" });
    const created = fakeNotebook({ name: "Projects", parent_id: root.id });
    appMock.CreateNotebook.mockResolvedValue(created);
    const { onCreated } = renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.new_notebook_menu_item" }));
    await user.type(
      await screen.findByRole("textbox", { name: "shell.new_notebook_placeholder" }),
      "Projects",
    );
    await user.click(await screen.findByRole("button", { name: "shell.new_notebook_button" }));

    expect(appMock.CreateNotebook).toHaveBeenCalledWith(root.id, "Projects");
    expect(onCreated).toHaveBeenCalledWith(created);
  });

  it("creates a note in a notebook from its row menu", async () => {
    const root = fakeNotebook({ name: "Work" });
    const { onCreateNote } = renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.new_note_button" }));

    expect(onCreateNote).toHaveBeenCalledWith(root.id);
  });

  it("deletes a notebook after confirming in the dialog", async () => {
    const root = fakeNotebook({ name: "Work" });
    appMock.SetNotebookDeleted.mockResolvedValue(undefined);
    const { onDeleted } = renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.delete_notebook" }));
    expect(
      await screen.findByRole("dialog", { name: "shell.delete_notebook: Work" }),
    ).toHaveTextContent("shell.delete_notebook_confirm");
    await user.click(await screen.findByRole("button", { name: "shell.delete_confirm_button" }));

    expect(appMock.SetNotebookDeleted).toHaveBeenCalledWith(root.id, true);
    expect(onDeleted).toHaveBeenCalledWith(root.id);
  });

  it("cancels a notebook deletion without calling the backend", async () => {
    const root = fakeNotebook({ name: "Work" });
    const { onDeleted } = renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.delete_notebook" }));
    await user.click(await screen.findByRole("button", { name: "common.cancel" }));

    expect(appMock.SetNotebookDeleted).not.toHaveBeenCalled();
    expect(onDeleted).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "shell.notebook_actions: Work" })).toBeInTheDocument();
  });

  it("shows an error when notebook deletion fails", async () => {
    const root = fakeNotebook({ name: "Work" });
    appMock.SetNotebookDeleted.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.delete_notebook" }));
    await user.click(await screen.findByRole("button", { name: "shell.delete_confirm_button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
  });

  it("renames a notebook via the rename dialog, pre-filled with its current name", async () => {
    const root = fakeNotebook({ name: "Work" });
    appMock.RenameNotebook.mockResolvedValue(undefined);
    const { onRenamed } = renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.rename_notebook" }));
    const input = await screen.findByRole("textbox", { name: "shell.new_notebook_placeholder" });
    expect(input).toHaveValue("Work");
    await user.clear(input);
    await user.type(input, "Projects");
    await user.click(await screen.findByRole("button", { name: "shell.rename_notebook_button" }));

    expect(appMock.RenameNotebook).toHaveBeenCalledWith(root.id, "Projects");
    expect(onRenamed).toHaveBeenCalledWith(root.id, "Projects");
  });

  it("shows an error when renaming a notebook fails", async () => {
    const root = fakeNotebook({ name: "Work" });
    appMock.RenameNotebook.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.rename_notebook" }));
    await user.click(await screen.findByRole("button", { name: "shell.rename_notebook_button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
  });

  it("reparents a notebook via the row menu's move dialog (the keyboard/menu alternative to drag)", async () => {
    const work = fakeNotebook({ name: "Work" });
    const personal = fakeNotebook({ name: "Personal" });
    appMock.MoveNotebook.mockResolvedValue(undefined);
    const { onMoved } = renderTree([work, personal]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.move_notebook_menu_item" }));
    const dialog = await screen.findByRole("dialog", { name: "shell.move_notebook_title: Work" });
    await user.click(within(dialog).getByRole("button", { name: "Personal" }));

    expect(appMock.MoveNotebook).toHaveBeenCalledWith(work.id, personal.id);
    expect(onMoved).toHaveBeenCalledWith(work.id, personal.id);
  });

  it("moving a notebook to the top level via the move dialog passes an empty parent", async () => {
    const child = fakeNotebook({ name: "Projects" });
    appMock.MoveNotebook.mockResolvedValue(undefined);
    const { onMoved } = renderTree([child]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Projects" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.move_notebook_menu_item" }));
    await user.click(await screen.findByRole("button", { name: "shell.no_parent_notebook" }));

    expect(appMock.MoveNotebook).toHaveBeenCalledWith(child.id, "");
    expect(onMoved).toHaveBeenCalledWith(child.id, "");
  });

  it("does not offer a notebook as a move target for itself", async () => {
    const root = fakeNotebook({ name: "Work" });
    renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.move_notebook_menu_item" }));

    const dialog = await screen.findByRole("dialog", { name: "shell.move_notebook_title: Work" });
    expect(within(dialog).queryByRole("button", { name: "Work" })).not.toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "shell.no_parent_notebook" })).toBeInTheDocument();
  });

  it("explains an invalid move (a cycle) instead of leaving a generic error", async () => {
    const root = fakeNotebook({ name: "Work" });
    const child = fakeNotebook({ name: "Projects", parent_id: root.id });
    appMock.MoveNotebook.mockRejectedValue(
      new Error(JSON.stringify({ code: "notebook_cycle", message: "store: notebook parent would create a cycle" })),
    );
    renderTree([root, child]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.notebook_actions: Work" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.move_notebook_menu_item" }));
    await user.click(await screen.findByRole("button", { name: "Projects" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.notebook_cycle");
  });

  it("persists notebook expansion across a remount", async () => {
    const root = fakeNotebook({ name: "Work" });
    const child = fakeNotebook({ name: "Projects", parent_id: root.id });
    const tree = (
      <I18nProvider>
        <NotebookTree
          notebooks={[root, child]}
          selectedId=""
          onSelect={vi.fn()}
          onCreateNote={vi.fn()}
          onCreated={vi.fn()}
          onRenamed={vi.fn()}
          onDeleted={vi.fn()}
          onMoved={vi.fn()}
          onNoteMoved={vi.fn()}
        />
      </I18nProvider>
    );
    mockLocaleCatalog();
    mockSettings();
    const { unmount } = render(tree);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "shell.expand_notebook" }));
    expect(await screen.findByRole("button", { name: "Projects" })).toBeInTheDocument();
    unmount();

    mockLocaleCatalog();
    mockSettings();
    render(tree);

    expect(await screen.findByRole("button", { name: "Projects" })).toBeInTheDocument();
  });
});

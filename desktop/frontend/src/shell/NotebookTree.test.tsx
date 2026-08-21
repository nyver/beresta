import { render, screen } from "@testing-library/react";
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
  const onCreated = vi.fn();
  const onDeleted = vi.fn();
  render(
    <I18nProvider>
      <NotebookTree
        notebooks={notebooks}
        selectedId={selectedId}
        onSelect={onSelect}
        onCreated={onCreated}
        onDeleted={onDeleted}
      />
    </I18nProvider>,
  );
  return { onSelect, onCreated, onDeleted };
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

  it("creates a new root notebook from the inline form", async () => {
    const created = fakeNotebook({ name: "Personal" });
    appMock.CreateNotebook.mockResolvedValue(created);
    const { onCreated } = renderTree([]);
    const user = userEvent.setup();

    await user.type(
      await screen.findByRole("textbox", { name: "shell.new_notebook_placeholder" }),
      "Personal",
    );
    await user.click(await screen.findByRole("button", { name: "shell.new_notebook_button" }));

    expect(appMock.CreateNotebook).toHaveBeenCalledWith("", "Personal");
    expect(onCreated).toHaveBeenCalledWith(created);
  });

  it("shows an error when notebook creation fails", async () => {
    appMock.CreateNotebook.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    renderTree([]);
    const user = userEvent.setup();

    await user.type(
      await screen.findByRole("textbox", { name: "shell.new_notebook_placeholder" }),
      "Personal",
    );
    await user.click(await screen.findByRole("button", { name: "shell.new_notebook_button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
  });

  it("deletes a notebook after the inline confirmation", async () => {
    const root = fakeNotebook({ name: "Work" });
    appMock.SetNotebookDeleted.mockResolvedValue(undefined);
    const { onDeleted } = renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.delete_notebook" }));
    await user.click(await screen.findByRole("button", { name: "shell.delete_confirm_button" }));

    expect(appMock.SetNotebookDeleted).toHaveBeenCalledWith(root.id, true);
    expect(onDeleted).toHaveBeenCalledWith(root.id);
  });

  it("cancels a notebook deletion without calling the backend", async () => {
    const root = fakeNotebook({ name: "Work" });
    const { onDeleted } = renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.delete_notebook" }));
    await user.click(await screen.findByRole("button", { name: "common.cancel" }));

    expect(appMock.SetNotebookDeleted).not.toHaveBeenCalled();
    expect(onDeleted).not.toHaveBeenCalled();
    expect(await screen.findByRole("button", { name: "shell.delete_notebook" })).toBeInTheDocument();
  });

  it("shows an error when notebook deletion fails", async () => {
    const root = fakeNotebook({ name: "Work" });
    appMock.SetNotebookDeleted.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    renderTree([root]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.delete_notebook" }));
    await user.click(await screen.findByRole("button", { name: "shell.delete_confirm_button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
  });
});

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { fakeNotebook, mockLocaleCatalog, mockSettings } from "../testUtils";
import { NotebookTree } from "./NotebookTree";

function renderTree(notebooks: ReturnType<typeof fakeNotebook>[], selectedId: string | null = "") {
  mockLocaleCatalog();
  mockSettings();
  const onSelect = vi.fn();
  render(
    <I18nProvider>
      <NotebookTree notebooks={notebooks} selectedId={selectedId} onSelect={onSelect} />
    </I18nProvider>,
  );
  return { onSelect };
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
});

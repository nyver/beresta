import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { appMock } from "../setupTests";
import { I18nProvider } from "../i18n";
import { fakeTag, mockLocaleCatalog, mockSettings } from "../testUtils";
import { TagList } from "./TagList";

function renderList(tags: ReturnType<typeof fakeTag>[], selectedId = "") {
  mockLocaleCatalog();
  mockSettings();
  const onSelect = vi.fn();
  const onCreated = vi.fn();
  const onDeleted = vi.fn();
  render(
    <I18nProvider>
      <TagList tags={tags} selectedId={selectedId} onSelect={onSelect} onCreated={onCreated} onDeleted={onDeleted} />
    </I18nProvider>,
  );
  return { onSelect, onCreated, onDeleted };
}

describe("TagList", () => {
  it("shows no tag rows, and no create form, until '+' is clicked", async () => {
    renderList([fakeTag({ deleted: true })]);
    const user = userEvent.setup();
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "shell.new_tag_placeholder" })).not.toBeInTheDocument();

    await user.click(await screen.findByRole("button", { name: "shell.tags_add_button" }));

    expect(await screen.findByRole("textbox", { name: "shell.new_tag_placeholder" })).toBeInTheDocument();
  });

  it("selecting a tag reports its id", async () => {
    const tag = fakeTag({ name: "urgent" });
    const { onSelect } = renderList([tag]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "urgent" }));
    expect(onSelect).toHaveBeenCalledWith(tag.id);
  });

  it("highlights the selected tag", async () => {
    const tag = fakeTag({ name: "urgent" });
    renderList([tag], tag.id);
    expect(await screen.findByRole("button", { name: "urgent" })).toHaveClass("selected");
  });

  it("creates a new tag from the inline form", async () => {
    const created = fakeTag({ name: "urgent" });
    appMock.CreateTag.mockResolvedValue(created);
    const { onCreated } = renderList([]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.tags_add_button" }));
    await user.type(await screen.findByRole("textbox", { name: "shell.new_tag_placeholder" }), "urgent");
    await user.click(await screen.findByRole("button", { name: "shell.new_tag_button" }));

    expect(appMock.CreateTag).toHaveBeenCalledWith("urgent");
    expect(onCreated).toHaveBeenCalledWith(created);
  });

  it("deletes a tag after the inline confirmation", async () => {
    const tag = fakeTag({ name: "urgent" });
    appMock.SetTagDeleted.mockResolvedValue(undefined);
    const { onDeleted } = renderList([tag]);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "shell.tag_actions: urgent" }));
    await user.click(await screen.findByRole("menuitem", { name: "shell.delete_tag" }));
    await user.click(await screen.findByRole("button", { name: "shell.delete_confirm_button" }));

    expect(appMock.SetTagDeleted).toHaveBeenCalledWith(tag.id, true);
    expect(onDeleted).toHaveBeenCalledWith(tag.id);
  });
});

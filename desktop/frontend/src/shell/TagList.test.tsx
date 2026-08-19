import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { fakeTag, mockLocaleCatalog, mockSettings } from "../testUtils";
import { TagList } from "./TagList";

describe("TagList", () => {
  it("renders nothing when there are no live tags", () => {
    mockLocaleCatalog();
    mockSettings();
    const { container } = render(
      <I18nProvider>
        <TagList tags={[fakeTag({ deleted: true })]} selectedId="" onSelect={vi.fn()} />
      </I18nProvider>,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("selecting a tag reports its id", async () => {
    mockLocaleCatalog();
    mockSettings();
    const tag = fakeTag({ name: "urgent" });
    const onSelect = vi.fn();
    render(
      <I18nProvider>
        <TagList tags={[tag]} selectedId="" onSelect={onSelect} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "urgent" }));
    expect(onSelect).toHaveBeenCalledWith(tag.id);
  });

  it("highlights the selected tag", async () => {
    mockLocaleCatalog();
    mockSettings();
    const tag = fakeTag({ name: "urgent" });
    render(
      <I18nProvider>
        <TagList tags={[tag]} selectedId={tag.id} onSelect={vi.fn()} />
      </I18nProvider>,
    );
    expect(await screen.findByRole("button", { name: "urgent" })).toHaveClass("selected");
  });
});

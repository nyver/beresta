import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { KebabMenu } from "./KebabMenu";

describe("KebabMenu", () => {
  it("returns focus to the trigger button when Escape closes the menu after Tab moved focus into it", async () => {
    render(<KebabMenu label="Note actions" items={[{ label: "Rename", onSelect: vi.fn() }]} />);
    const user = userEvent.setup();
    const trigger = screen.getByRole("button", { name: "Note actions" });

    await user.click(trigger);
    await user.tab();
    expect(await screen.findByRole("menuitem", { name: "Rename" })).toHaveFocus();

    await user.keyboard("{Escape}");

    // Without this, closing the menu while a menu item is focused would
    // unmount that element and drop focus to the document body instead
    // (task 8.4's "logical tab/focus restoration").
    expect(trigger).toHaveFocus();
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("returns focus to the trigger button after selecting a menu item", async () => {
    const onSelect = vi.fn();
    render(<KebabMenu label="Note actions" items={[{ label: "Rename", onSelect }]} />);
    const user = userEvent.setup();
    const trigger = screen.getByRole("button", { name: "Note actions" });

    await user.click(trigger);
    await user.click(await screen.findByRole("menuitem", { name: "Rename" }));

    expect(onSelect).toHaveBeenCalled();
    expect(trigger).toHaveFocus();
  });

  it("does not steal focus back to the trigger on an outside click", async () => {
    render(
      <div>
        <KebabMenu label="Note actions" items={[{ label: "Rename", onSelect: vi.fn() }]} />
        <button type="button">Elsewhere</button>
      </div>,
    );
    const user = userEvent.setup();
    const trigger = screen.getByRole("button", { name: "Note actions" });
    await user.click(trigger);
    expect(await screen.findByRole("menu")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Elsewhere" }));

    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Elsewhere" })).toHaveFocus();
  });
});

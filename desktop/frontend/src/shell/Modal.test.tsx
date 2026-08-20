import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";

import { I18nProvider } from "../i18n";
import { mockLocaleCatalog } from "../testUtils";
import { Modal } from "./Modal";

function ModalHarness() {
  const [open, setOpen] = useState(false);
  return (
    <I18nProvider>
      <button type="button" onClick={() => setOpen(true)}>
        Open dialog
      </button>
      {open ? (
        <Modal title="Accessible dialog" onClose={() => setOpen(false)}>
          <button type="button">Dialog action</button>
        </Modal>
      ) : null}
    </I18nProvider>
  );
}

describe("Modal accessibility", () => {
  it("names the dialog, traps keyboard focus, closes with Escape, and restores focus", async () => {
    mockLocaleCatalog();
    const user = userEvent.setup();
    render(<ModalHarness />);

    const opener = screen.getByRole("button", { name: "Open dialog" });
    await user.click(opener);

    const dialog = await screen.findByRole("dialog", { name: "Accessible dialog" });
    const close = screen.getByRole("button", { name: "common.close" });
    const action = screen.getByRole("button", { name: "Dialog action" });
    expect(close).toHaveFocus();

    await user.keyboard("{Shift>}{Tab}{/Shift}");
    expect(action).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();

    await user.keyboard("{Escape}");
    expect(dialog).not.toBeInTheDocument();
    expect(opener).toHaveFocus();
  });
});

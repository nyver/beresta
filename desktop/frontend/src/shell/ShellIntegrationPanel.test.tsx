import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { mockAutostartStatus, mockLocaleCatalog, mockSettings } from "../testUtils";
import { ShellIntegrationPanel } from "./ShellIntegrationPanel";

function renderPanel() {
  mockLocaleCatalog();
  mockSettings({ quick_note_hotkey: "Ctrl+Shift+N", autostart_enabled: false });
  mockAutostartStatus();
  render(
    <I18nProvider>
      <ShellIntegrationPanel />
    </I18nProvider>,
  );
}

describe("ShellIntegrationPanel", () => {
  it("shows the persisted hotkey and autostart setting", async () => {
    renderPanel();

    expect(await screen.findByDisplayValue("Ctrl+Shift+N")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "shellintegration.autostart_label" })).not.toBeChecked();
  });

  it("saves an edited hotkey through UpdateSettings", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.UpdateSettings.mockResolvedValue({
      language: "en",
      last_database_path: "",
      auto_lock_minutes: 15,
      backup_directory: "C:\\backups",
      quick_note_hotkey: "Ctrl+Alt+Q",
      autostart_enabled: false,
    });

    const input = await screen.findByDisplayValue("Ctrl+Shift+N");
    await user.clear(input);
    await user.type(input, "Ctrl+Alt+Q");
    await user.click(screen.getByRole("button", { name: "shellintegration.save_hotkey_button" }));

    await waitFor(() =>
      expect(appMock.UpdateSettings).toHaveBeenCalledWith(
        expect.objectContaining({ quick_note_hotkey: "Ctrl+Alt+Q" }),
      ),
    );
  });

  it("shows a validation error when saving an invalid hotkey fails", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.UpdateSettings.mockRejectedValue(
      new Error(JSON.stringify({ code: "invalid_input", message: "bad hotkey" })),
    );

    await screen.findByDisplayValue("Ctrl+Shift+N");
    await user.click(screen.getByRole("button", { name: "shellintegration.save_hotkey_button" }));

    expect(await screen.findByText("errors.invalid_input")).toBeInTheDocument();
  });

  it("toggles autostart through UpdateSettings and surfaces a conflicting entry", async () => {
    renderPanel();
    const user = userEvent.setup();
    appMock.UpdateSettings.mockResolvedValue({
      language: "en",
      last_database_path: "",
      auto_lock_minutes: 15,
      backup_directory: "C:\\backups",
      quick_note_hotkey: "Ctrl+Shift+N",
      autostart_enabled: true,
    });
    appMock.AutostartStatus.mockResolvedValue({ enabled: false, conflict_path: "C:\\Old\\beresta.exe" });

    const checkbox = await screen.findByRole("checkbox", { name: "shellintegration.autostart_label" });
    await user.click(checkbox);

    await waitFor(() =>
      expect(appMock.UpdateSettings).toHaveBeenCalledWith(expect.objectContaining({ autostart_enabled: true })),
    );
    expect(await screen.findByText("shellintegration.autostart_conflict")).toBeInTheDocument();
  });
});

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { fakeAccountInfo, mockAutostartStatus, mockLocaleCatalog, mockSettings } from "../testUtils";
import { SettingsPanel } from "./SettingsPanel";

function renderGeneralSettings(locale: "en" | "ru" = "en") {
  mockLocaleCatalog(locale);
  mockSettings({ language: locale });
  mockAutostartStatus();
  render(
    <I18nProvider>
      <SettingsPanel
        activeGroup="general"
        onGroupChange={() => {}}
        account={fakeAccountInfo()}
        autoLockMinutes={15}
        onAutoLockChange={() => {}}
        onRestored={() => {}}
        onImported={() => {}}
      />
    </I18nProvider>,
  );
}

describe("SettingsPanel general tab", () => {
  it("shows the language control with the persisted language selected", async () => {
    renderGeneralSettings("ru");

    expect(await screen.findByLabelText("settings.language_label")).toHaveValue("ru");
  });

  it("persists a language change through UpdateSettings and reloads the catalog", async () => {
    renderGeneralSettings("en");
    const user = userEvent.setup();

    await user.selectOptions(await screen.findByLabelText("settings.language_label"), "ru");

    await waitFor(() =>
      expect(appMock.UpdateSettings).toHaveBeenCalledWith(expect.objectContaining({ language: "ru" })),
    );
    expect(appMock.Catalog).toHaveBeenCalledWith("ru");
  });
});

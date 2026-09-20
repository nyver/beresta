import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { mockLocaleCatalog } from "../testUtils";
import { DataCheckPanel } from "./DataCheckPanel";
import { main } from "../../wailsjs/go/models";

function fakeReport(overrides: Partial<main.DataCheckReportDTO> = {}): main.DataCheckReportDTO {
  return main.DataCheckReportDTO.createFrom({
    issue: "none",
    healthy: true,
    checked_at_unix_ms: 0,
    ...overrides,
  });
}

function renderPanel() {
  mockLocaleCatalog();
  render(
    <I18nProvider>
      <DataCheckPanel />
    </I18nProvider>,
  );
}

describe("DataCheckPanel", () => {
  it("does not run the check until the button is pressed", () => {
    renderPanel();
    expect(appMock.RunDataCheck).not.toHaveBeenCalled();
  });

  it("shows a healthy result", async () => {
    appMock.RunDataCheck.mockResolvedValue(fakeReport());
    renderPanel();

    await userEvent.click(screen.getByRole("button", { name: "data_check.action_button" }));

    expect(await screen.findByText("data_check.issue_none")).toBeInTheDocument();
  });

  it("shows one actionable summary without itemizing internal maintenance jobs", async () => {
    appMock.RunDataCheck.mockResolvedValue(
      fakeReport({ issue: "backup_needs_attention", healthy: false }),
    );
    renderPanel();

    await userEvent.click(screen.getByRole("button", { name: "data_check.action_button" }));

    expect(await screen.findByText("data_check.issue_backup_needs_attention")).toBeInTheDocument();
  });

  it("surfaces a failed check as an error", async () => {
    appMock.RunDataCheck.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "boom" })),
    );
    renderPanel();

    await userEvent.click(screen.getByRole("button", { name: "data_check.action_button" }));

    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });
});

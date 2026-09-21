import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { mockLocaleCatalog } from "../testUtils";
import { DiagnosticsPanel } from "./DiagnosticsPanel";
import { main } from "../../wailsjs/go/models";

function fakeSummary(overrides: Partial<main.DiagnosticSummaryDTO> = {}): main.DiagnosticSummaryDTO {
  return main.DiagnosticSummaryDTO.createFrom({
    app_version: "1.2.3",
    platform: "windows",
    sync_configured: true,
    last_successful_sync_unix_ms: 0,
    pending_count: 2,
    connection_state: "offline",
    backup: { health: "healthy", last_verified_unix_ms: 0, location: "" },
    storage_usage_bytes: 2048,
    database: "ok",
    update: "up_to_date",
    ...overrides,
  });
}

function fakeTechnical(overrides: Partial<main.TechnicalDiagnosticsDTO> = {}): main.TechnicalDiagnosticsDTO {
  return main.TechnicalDiagnosticsDTO.createFrom({
    workspace_id: "ws-1",
    device_id: "device-1",
    last_error_class: "transient_transport",
    pending_operation_count: 2,
    quarantined_operation_ids: [],
    cursor_sequence: 5,
    cursor_epoch: 1,
    retry_count: 1,
    retry_in_ms: 0,
    transport_protocol: "https",
    transport_security_mode: "pinned",
    transport_url: "https://home.example",
    migration_version: 12,
    rotation_pending: false,
    ...overrides,
  });
}

function renderPanel() {
  mockLocaleCatalog();
  render(
    <I18nProvider>
      <DiagnosticsPanel />
    </I18nProvider>,
  );
}

describe("DiagnosticsPanel", () => {
  it("does not fetch diagnostics until the user expands the section", () => {
    renderPanel();
    expect(appMock.DiagnosticSummary).not.toHaveBeenCalled();
  });

  it("loads and shows the summary once expanded", async () => {
    appMock.DiagnosticSummary.mockResolvedValue(fakeSummary());
    renderPanel();

    await userEvent.click(screen.getByRole("button", { name: "diagnostics.title" }));

    expect(await screen.findByText("1.2.3")).toBeInTheDocument();
    expect(screen.getByText("windows")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
  });

  it("loads technical details only once that section is expanded, then shows them", async () => {
    appMock.DiagnosticSummary.mockResolvedValue(fakeSummary());
    appMock.TechnicalDiagnostics.mockResolvedValue(fakeTechnical());
    renderPanel();

    await userEvent.click(screen.getByRole("button", { name: "diagnostics.title" }));
    await screen.findByText("1.2.3");
    expect(appMock.TechnicalDiagnostics).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "diagnostics.show_technical_details_button" }));

    expect(await screen.findByText("ws-1")).toBeInTheDocument();
    expect(screen.getByText("device-1")).toBeInTheDocument();
    expect(screen.getByText("5@1")).toBeInTheDocument();
    expect(screen.getByText("diagnostics.rotation_pending_label")).toBeInTheDocument();
    expect(screen.getByText("diagnostics.no")).toBeInTheDocument();
  });

  it("shows a pending workspace key rotation in technical details", async () => {
    appMock.DiagnosticSummary.mockResolvedValue(fakeSummary());
    appMock.TechnicalDiagnostics.mockResolvedValue(fakeTechnical({ rotation_pending: true }));
    renderPanel();

    await userEvent.click(screen.getByRole("button", { name: "diagnostics.title" }));
    await screen.findByText("1.2.3");
    await userEvent.click(screen.getByRole("button", { name: "diagnostics.show_technical_details_button" }));

    await screen.findByText("ws-1");
    const label = screen.getByText("diagnostics.rotation_pending_label");
    expect(label.nextElementSibling).toHaveTextContent("diagnostics.yes");
  });

  it("copies the sanitized bundle to the clipboard and shows confirmation", async () => {
    appMock.DiagnosticSummary.mockResolvedValue(fakeSummary());
    appMock.CopyDiagnostics.mockResolvedValue("Beresta diagnostics\n\nApp version: 1.2.3\n");
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    renderPanel();

    await userEvent.click(screen.getByRole("button", { name: "diagnostics.title" }));
    await screen.findByText("1.2.3");
    await userEvent.click(screen.getByRole("button", { name: "diagnostics.copy_button" }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith("Beresta diagnostics\n\nApp version: 1.2.3\n"));
    expect(await screen.findByRole("button", { name: "diagnostics.copied_label" })).toBeInTheDocument();
  });
});

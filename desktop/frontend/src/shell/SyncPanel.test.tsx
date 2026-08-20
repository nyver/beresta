import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock, runtimeMock } from "../setupTests";
import { mockLocaleCatalog, mockSyncStatus } from "../testUtils";
import { SyncPanel } from "./SyncPanel";

function renderPanel(status = "disabled") {
  mockLocaleCatalog();
  mockSyncStatus(status);
  render(
    <I18nProvider>
      <SyncPanel deviceId="device-123" />
    </I18nProvider>,
  );
}

describe("SyncPanel", () => {
  it.each(["disabled", "offline", "active", "current", "failed"])(
    "renders the explicit %s state",
    async (status) => {
      renderPanel(status);

      expect(await screen.findByText(`sync.status_${status}`)).toBeInTheDocument();
      expect(screen.getByText(`sync.status_${status}_description`)).toBeInTheDocument();
    },
  );

  it("updates from the shared synchronization event", async () => {
    renderPanel();
    await screen.findByText("sync.status_disabled");
    await waitFor(() => expect(runtimeMock.EventsOnMultiple).toHaveBeenCalled());
    const [, onStatus] =
      runtimeMock.EventsOnMultiple.mock.calls.find(([name]) => name === "sync:status") ?? [];

    act(() => onStatus?.("offline"));

    expect(await screen.findByText("sync.status_offline")).toBeInTheDocument();
  });

  it("shows only the real local device and current-phase placeholders", async () => {
    renderPanel();

    expect(await screen.findByText("device-123")).toBeInTheDocument();
    expect(screen.getByText("sync.journal_empty")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "sync.connect_button" })).toBeDisabled();
  });

  it("offers a retry when status loading fails", async () => {
    mockLocaleCatalog();
    appMock.SyncStatus.mockRejectedValueOnce(new Error("bridge failed")).mockResolvedValue("current");
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );

    await userEvent.setup().click(await screen.findByRole("button", { name: "common.retry" }));

    expect(await screen.findByText("sync.status_current")).toBeInTheDocument();
  });
});

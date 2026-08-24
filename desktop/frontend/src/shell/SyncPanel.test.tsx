import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

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

  it("shows the diagnostic detail for a failed synchronization cycle", async () => {
    mockLocaleCatalog();
    mockSyncStatus("failed");
    appMock.SyncError.mockResolvedValue("sync: review snapshot: signature verification failed");
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );

    expect(await screen.findByText("sync.error_details_label")).toBeInTheDocument();
    expect(screen.getByText("sync: review snapshot: signature verification failed")).toBeInTheDocument();
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

  it("shows this account's identity code and copies it on request", async () => {
    // @testing-library/user-event installs its own navigator.clipboard stub
    // as soon as setup() runs, which would shadow ours below; dispatch a
    // plain DOM click instead so the component's own handler reaches the
    // clipboard mock this test actually asserts against.
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    renderPanel("active");

    const identityField = (await screen.findByLabelText(
      "sync.export_identity_title",
    )) as HTMLTextAreaElement;
    expect(identityField.value).toBe("beresta://identity?user=test&key=00");

    const copyButton = screen.getByRole("button", { name: "sync.copy_button" }) as HTMLButtonElement;
    await act(async () => {
      copyButton.click();
      await Promise.resolve();
    });

    expect(writeText).toHaveBeenCalledWith("beresta://identity?user=test&key=00");
    expect(await screen.findByRole("button", { name: "sync.copied_label" })).toBeInTheDocument();
  });

  it("shares the workspace from a pasted identity code and shows the resulting grant code", async () => {
    appMock.ShareWorkspace.mockResolvedValue("beresta://grant?workspace=w&key=k&authority=a&sig=s");
    renderPanel("active");
    const user = userEvent.setup();

    await user.type(
      await screen.findByLabelText("sync.paste_identity_label"),
      "beresta://identity?user=peer&key=00",
    );
    await user.click(screen.getByRole("button", { name: "sync.share_workspace_button" }));

    expect(appMock.ShareWorkspace).toHaveBeenCalledWith("beresta://identity?user=peer&key=00");
    const grantField = (await screen.findByLabelText("sync.grant_code_label")) as HTMLTextAreaElement;
    expect(grantField.value).toBe("beresta://grant?workspace=w&key=k&authority=a&sig=s");
  });

  it("joins a shared workspace from a pasted grant code and notifies the parent", async () => {
    appMock.AcceptWorkspaceGrant.mockResolvedValue({
      workspace_id: "shared-workspace",
      role: "member",
      active: true,
      member_count: 2,
    });
    const onWorkspaceChanged = vi.fn();
    mockLocaleCatalog();
    mockSyncStatus("active");
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" onWorkspaceChanged={onWorkspaceChanged} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await user.type(
      await screen.findByLabelText("sync.paste_grant_label"),
      "beresta://grant?workspace=w&key=k&authority=a&sig=s",
    );
    await user.click(screen.getByRole("button", { name: "sync.join_workspace_button" }));

    expect(appMock.AcceptWorkspaceGrant).toHaveBeenCalledWith(
      "beresta://grant?workspace=w&key=k&authority=a&sig=s",
    );
    await waitFor(() => expect(onWorkspaceChanged).toHaveBeenCalled());
  });

  it("lists held workspaces and switches the active one", async () => {
    mockLocaleCatalog();
    mockSyncStatus("active");
    // mockSyncStatus defaults ListWorkspaces to []; override it after, since
    // renderPanel would otherwise re-apply that default on top of this.
    appMock.ListWorkspaces.mockResolvedValue([
      { workspace_id: "own-workspace", role: "owner", active: true },
      { workspace_id: "shared-workspace", role: "member", active: false, member_count: 2 },
    ]);
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );

    expect(await screen.findByText("own-workspace")).toBeInTheDocument();
    expect(screen.getByText("shared-workspace")).toBeInTheDocument();
    expect(screen.getByText("sync.workspace_active_badge")).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("button", { name: "sync.workspace_switch_button" }));

    expect(appMock.SetActiveWorkspace).toHaveBeenCalledWith("shared-workspace");
  });

  it("lets an owner disconnect an active workspace client", async () => {
    mockLocaleCatalog();
    mockSyncStatus("active");
    appMock.ListWorkspaces.mockResolvedValue([
      { workspace_id: "own-workspace", role: "owner", active: true, member_count: 2 },
    ]);
    appMock.ListWorkspaceMembers.mockResolvedValue([
      { user_id: "owner-id", display_name: "Desktop owner", role: "owner" },
      { user_id: "mobile-id", display_name: "Mobile client", role: "member" },
    ]);
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );

    expect(await screen.findByText("Mobile client")).toBeInTheDocument();
    await userEvent.setup().click(
      screen.getByRole("button", { name: "sync.workspace_remove_member_button" }),
    );
    expect(appMock.RevokeWorkspaceMember).toHaveBeenCalledWith(
      "own-workspace",
      "mobile-id",
    );
  });
});

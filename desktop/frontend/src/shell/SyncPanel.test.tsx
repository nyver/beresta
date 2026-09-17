import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock, runtimeMock } from "../setupTests";
import { mockLocaleCatalog, mockSyncSummary } from "../testUtils";
import { SyncPanel } from "./SyncPanel";

function renderPanel(status = "local_only") {
  mockLocaleCatalog();
  mockSyncSummary(status);
  render(
    <I18nProvider>
      <SyncPanel deviceId="device-123" />
    </I18nProvider>,
  );
}

describe("SyncPanel", () => {
  it.each(["local_only", "offline", "active", "current", "pending", "retrying", "action_required"])(
    "renders the explicit %s state",
    async (status) => {
      renderPanel(status);

      expect(await screen.findByText(`sync.status_${status}`)).toBeInTheDocument();
      expect(screen.getByText(`sync.status_${status}_description`)).toBeInTheDocument();
    },
  );

  it("shows the connected endpoint and applies a replacement server and certificate policy", async () => {
    mockLocaleCatalog();
    mockSyncSummary("current");
    appMock.SyncConnectionInfo.mockResolvedValue({
      enabled: true,
      url: "https://old.example.com",
      protocol: "https",
      security_mode: "trusted",
      fingerprint: "",
    });
    appMock.ConnectServer.mockResolvedValue({
      enabled: true,
      url: "https://new.example.com",
      protocol: "https",
      security_mode: "pinned",
      fingerprint: "ab12",
    });
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    expect(await screen.findByText("https://old.example.com")).toBeInTheDocument();
    expect(screen.getByText("sync.protocol_https")).toBeInTheDocument();
    expect(screen.getAllByText("sync.verification_trusted")).toHaveLength(2);
    const urlField = screen.getByLabelText("sync.url_label");
    await user.clear(urlField);
    await user.type(urlField, "https://new.example.com");
    await user.selectOptions(screen.getByLabelText("sync.verification_label"), "pinned");
    await user.type(screen.getByLabelText("sync.server_fingerprint_label"), "ab12");
    await user.click(screen.getByRole("button", { name: "sync.change_server_button" }));

    expect(appMock.ConnectServer).toHaveBeenCalledWith(expect.objectContaining({
      url: "https://new.example.com",
      security_mode: "pinned",
      fingerprint: "ab12",
    }));
    expect(await screen.findByText("https://new.example.com")).toBeInTheDocument();
  });

  it("updates from the shared synchronization event", async () => {
    renderPanel();
    await screen.findByText("sync.status_local_only");
    await waitFor(() => expect(runtimeMock.EventsOnMultiple).toHaveBeenCalled());
    const [, onSummary] =
      runtimeMock.EventsOnMultiple.mock.calls.find(([name]) => name === "sync:summary") ?? [];

    // The event itself carries no payload - it signals a re-fetch, which is
    // where the new "offline" state actually comes from.
    mockSyncSummary("offline");
    act(() => onSummary?.());

    expect(await screen.findByText("sync.status_offline")).toBeInTheDocument();
  });

  it("shows the durable pending count while synchronization is queued", async () => {
    mockLocaleCatalog();
    mockSyncSummary("pending", { pending_count: 3 });
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );

    expect(await screen.findByText("sync.pending_count_label")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
  });

  it("shows only the real local device and current-phase placeholders", async () => {
    renderPanel();

    expect(await screen.findByText("device-123")).toBeInTheDocument();
    expect(screen.getByText("sync.journal_empty")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "sync.connect_button" })).toBeDisabled();
  });

  it("offers a retry when status loading fails", async () => {
    mockLocaleCatalog();
    appMock.SyncSummary.mockRejectedValueOnce(new Error("bridge failed")).mockResolvedValue({
      state: "current",
      pending_count: 0,
      last_success_unix_ms: 0,
      retry_in_ms: 0,
      unsafe_count: 0,
      action_required: "none",
    });
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
    mockSyncSummary("active");
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
    mockSyncSummary("active");
    // mockSyncSummary defaults ListWorkspaces to []; override it after, since
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

  it("flushes the open note's editor before switching the active workspace", async () => {
    mockLocaleCatalog();
    mockSyncSummary("active");
    appMock.ListWorkspaces.mockResolvedValue([
      { workspace_id: "own-workspace", role: "owner", active: true },
      { workspace_id: "shared-workspace", role: "member", active: false, member_count: 2 },
    ]);
    const order: string[] = [];
    appMock.SetActiveWorkspace.mockImplementation(async () => {
      order.push("switch");
    });
    const onBeforeWorkspaceSwitch = vi.fn(async () => {
      order.push("flush");
    });
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" onBeforeWorkspaceSwitch={onBeforeWorkspaceSwitch} />
      </I18nProvider>,
    );

    await screen.findByText("own-workspace");
    await userEvent.setup().click(screen.getByRole("button", { name: "sync.workspace_switch_button" }));

    await waitFor(() => expect(appMock.SetActiveWorkspace).toHaveBeenCalled());
    expect(onBeforeWorkspaceSwitch).toHaveBeenCalled();
    // The pending local edit must reach durable storage before the
    // workspace it belongs to is replaced - never the other way around.
    expect(order).toEqual(["flush", "switch"]);
  });

  it("lets an owner disconnect an active workspace client", async () => {
    mockLocaleCatalog();
    mockSyncSummary("active");
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

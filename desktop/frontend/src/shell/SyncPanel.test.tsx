import { act, render, screen, waitFor, within } from "@testing-library/react";
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
    expect(screen.getAllByText("sync.verification_trusted")).toHaveLength(1);
    await user.click(screen.getByRole("button", { name: "sync.advanced_setup_title" }));
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

  it("connects from a pasted connection code without exposing the advanced fields", async () => {
    mockLocaleCatalog();
    mockSyncSummary("local_only");
    appMock.ConnectServer.mockResolvedValue({
      enabled: true,
      url: "https://code.example.com",
      protocol: "https",
      security_mode: "pinned",
      fingerprint: "cd34",
    });
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    expect(screen.queryByLabelText("sync.url_label")).not.toBeInTheDocument();
    await user.type(
      screen.getByLabelText("sync.qr_label"),
      "beresta://connect?url=https://code.example.com&invite=abc&fingerprint=cd34&mode=pinned",
    );
    await user.click(screen.getByRole("button", { name: "sync.connect_code_button" }));

    expect(appMock.ConnectServer).toHaveBeenCalledWith(expect.objectContaining({
      url: "",
      invite_code: "",
      fingerprint: "",
      security_mode: "",
      qr_code: "beresta://connect?url=https://code.example.com&invite=abc&fingerprint=cd34&mode=pinned",
    }));
    expect(await screen.findByText("https://code.example.com")).toBeInTheDocument();
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

    // The raw device ID is a protocol identifier and stays out of this
    // primary list (specs/identity-and-sharing's "Understandable device
    // inventory"); "This device" plus its platform is what identifies it.
    expect(await screen.findByText("sync.this_device")).toBeInTheDocument();
    expect(screen.getByText("sync.journal_empty")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "sync.connect_code_button" })).toBeDisabled();

    await userEvent.setup().click(screen.getByRole("button", { name: "sync.advanced_setup_title" }));
    expect(screen.getByRole("button", { name: "sync.connect_button" })).toBeDisabled();
  });

  it("localizes a quarantined operation's rejection reason instead of showing the raw class", async () => {
    mockLocaleCatalog();
    mockSyncSummary("current");
    appMock.ListSyncQuarantine.mockResolvedValue([
      { operation_id: "op-known", sequence: 1, reason: "op_id_reuse", received_unix_ms: 0 },
      { operation_id: "op-unknown", sequence: 2, reason: "some_future_class", received_unix_ms: 0 },
    ]);
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );

    expect(await screen.findByText("sync.quarantine_reason_op_id_reuse")).toBeInTheDocument();
    expect(screen.getByText("sync.quarantine_reason_unknown")).toBeInTheDocument();
    expect(screen.queryByText("op_id_reuse")).not.toBeInTheDocument();
    expect(screen.queryByText("some_future_class")).not.toBeInTheDocument();
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

  it("shows this account's identity as a QR code by default and reveals the raw text on request", async () => {
    // @testing-library/user-event installs its own navigator.clipboard stub
    // as soon as setup() runs, which would shadow ours below; dispatch a
    // plain DOM click instead so the component's own handler reaches the
    // clipboard mock this test actually asserts against.
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    renderPanel("active");

    // task 7.7: the raw identity code is not primary-flow content - only
    // its QR rendering is, until "Show code" is pressed.
    expect(await screen.findByAltText("sync.qr_alt_identity")).toBeInTheDocument();
    expect(screen.queryByLabelText("sync.export_identity_title")).not.toBeInTheDocument();

    // Plain DOM click, not userEvent.setup() (see the clipboard comment
    // above): setup() would install its own clipboard stub right here,
    // before the copy button below ever gets a chance to run.
    await act(async () => {
      screen.getByRole("button", { name: "sync.show_code_button" }).click();
    });

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

  it("shares the workspace from a pasted identity code only after explicit confirmation, then shows a QR success state", async () => {
    appMock.ShareWorkspace.mockResolvedValue("beresta://grant?workspace=w&key=k&authority=a&sig=s");
    renderPanel("active");
    const user = userEvent.setup();

    await user.type(
      await screen.findByLabelText("sync.paste_identity_label"),
      "beresta://identity?user=peer&key=00",
    );
    await user.click(screen.getByRole("button", { name: "sync.share_workspace_button" }));

    // Not shared yet: an explicit confirmation step sits between pasting
    // the code and actually granting access (task 7.7).
    expect(appMock.ShareWorkspace).not.toHaveBeenCalled();
    expect(await screen.findByText("sync.share_confirm_description")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "sync.share_confirm_button" }));

    expect(appMock.ShareWorkspace).toHaveBeenCalledWith("beresta://identity?user=peer&key=00");
    const successText = await screen.findByText("sync.share_success_description");
    expect(await screen.findByAltText("sync.qr_alt_grant")).toBeInTheDocument();
    // The grant code text itself stays behind "Show code" here too. Both
    // the identity section above and this one now render their own "Show
    // code" button, so this one is scoped to the share section's own form.
    expect(screen.queryByLabelText("sync.grant_code_label")).not.toBeInTheDocument();
    const shareForm = successText.closest(".sync-connect-form") as HTMLElement;

    await user.click(within(shareForm).getByRole("button", { name: "sync.show_code_button" }));
    const grantField = (await within(shareForm).findByLabelText(
      "sync.grant_code_label",
    )) as HTMLTextAreaElement;
    expect(grantField.value).toBe("beresta://grant?workspace=w&key=k&authority=a&sig=s");
  });

  it("joins a shared workspace from a pasted grant code only after explicit confirmation, then shows a plain-language success state", async () => {
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

    expect(appMock.AcceptWorkspaceGrant).not.toHaveBeenCalled();
    expect(await screen.findByText("sync.join_confirm_description")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "sync.join_confirm_button" }));

    expect(appMock.AcceptWorkspaceGrant).toHaveBeenCalledWith(
      "beresta://grant?workspace=w&key=k&authority=a&sig=s",
    );
    await waitFor(() => expect(onWorkspaceChanged).toHaveBeenCalled());
    expect(await screen.findByText("sync.join_success_description")).toBeInTheDocument();
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

  it("lets an owner disconnect an active workspace client only after confirming the revocation limitation disclosure", async () => {
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
    const user = userEvent.setup();

    expect(await screen.findByText("Mobile client")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "sync.workspace_remove_member_button" }));

    // Not revoked yet: the disclosure explains the future-access-only
    // guarantee before the actual RevokeWorkspaceMember call runs.
    expect(appMock.RevokeWorkspaceMember).not.toHaveBeenCalled();
    expect(await screen.findByText("sync.revoke_confirm_description")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "sync.revoke_confirm_button" }));

    expect(appMock.RevokeWorkspaceMember).toHaveBeenCalledWith(
      "own-workspace",
      "mobile-id",
    );
  });

  it("shows a revoked workspace member as disconnected instead of removing them from the list", async () => {
    mockLocaleCatalog();
    mockSyncSummary("active");
    appMock.ListWorkspaces.mockResolvedValue([
      { workspace_id: "own-workspace", role: "owner", active: true, member_count: 2 },
    ]);
    appMock.ListWorkspaceMembers.mockResolvedValue([
      { user_id: "owner-id", display_name: "Desktop owner", role: "owner" },
      { user_id: "mobile-id", display_name: "Mobile client", role: "member", revoked_at: "2026-01-01T00:00:00Z" },
    ]);
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );

    expect(await screen.findByText("Mobile client")).toBeInTheDocument();
    expect(screen.getByText("sync.device_revoked_badge")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "sync.workspace_remove_member_button" })).not.toBeInTheDocument();
  });

  it("shows understandable device names, platform, last seen, and access state, hiding the raw device ID", async () => {
    mockLocaleCatalog();
    mockSyncSummary("active");
    appMock.ListSyncDevices.mockResolvedValue([
      { device_id: "device-123", display_name: "This device", platform: "windows", created_at: "2026-01-01T00:00:00Z" },
      {
        device_id: "device-456", display_name: "Kitchen tablet", platform: "android",
        created_at: "2026-01-01T00:00:00Z", last_seen_at: "2026-02-03T04:05:00Z",
      },
      { device_id: "device-789", display_name: "Old laptop", created_at: "2026-01-01T00:00:00Z", revoked_at: "2026-02-01T00:00:00Z" },
    ]);
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );

    expect(await screen.findByText("Kitchen tablet")).toBeInTheDocument();
    expect(screen.getByText("sync.device_platform_android", { exact: false })).toBeInTheDocument();
    expect(screen.getByText("sync.device_active_badge")).toBeInTheDocument();
    expect(screen.getByText("Old laptop")).toBeInTheDocument();
    expect(screen.getByText("sync.device_revoked_badge")).toBeInTheDocument();
    // Platform label for a device with no recorded platform (an older
    // registration, or a build predating this field) degrades to unknown
    // rather than showing nothing or a raw empty string.
    expect(screen.getByText("sync.device_platform_unknown", { exact: false })).toBeInTheDocument();
    // The device_id itself is a protocol identifier and must not appear in
    // this primary list (specs/identity-and-sharing's "Understandable
    // device inventory").
    expect(screen.queryByText("device-456")).not.toBeInTheDocument();
    expect(screen.queryByText("device-789")).not.toBeInTheDocument();
  });

  it("disconnects a device only after confirming the revocation limitation disclosure", async () => {
    mockLocaleCatalog();
    mockSyncSummary("active");
    appMock.ListSyncDevices.mockResolvedValue([
      { device_id: "device-123", display_name: "This device", platform: "windows", created_at: "2026-01-01T00:00:00Z" },
      { device_id: "device-456", display_name: "Kitchen tablet", platform: "android", created_at: "2026-01-01T00:00:00Z" },
    ]);
    render(
      <I18nProvider>
        <SyncPanel deviceId="device-123" />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "sync.revoke_device_button" }));

    expect(appMock.RevokeSyncDevice).not.toHaveBeenCalled();
    expect(await screen.findByText("sync.revoke_confirm_description")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Kitchen tablet/ })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "sync.revoke_confirm_button" }));

    expect(appMock.RevokeSyncDevice).toHaveBeenCalledWith("device-456");
  });
});

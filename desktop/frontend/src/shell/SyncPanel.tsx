import { useCallback, useEffect, useState } from "react";

import {
  acceptWorkspaceGrant,
  connectServer,
  diagnoseServer,
  disableServer,
  exportIdentity,
  listSyncDevices,
  listSyncQuarantine,
  listWorkspaceMembers,
  listWorkspaces,
  revokeWorkspaceMember,
  retrySyncQuarantine,
  revokeSyncDevice,
  setActiveWorkspace,
  shareWorkspace,
  syncConnectionInfo,
  syncSummary,
  type ConnectServerRequest,
  type QuarantineEntry,
  type ServerConnectionInfo,
  type ServerDiagnostics,
  type SyncDevice,
  type SyncState,
  type WorkspaceSummary,
  type WorkspaceMember,
  unwrapError,
} from "../api";
import { useI18n } from "../i18n";
import { EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";
import { Modal } from "./Modal";
import { QrCode } from "./QrCode";
import { ErrorState, LoadingState } from "./StatusState";

const EVENT_SYNC_SUMMARY = "sync:summary";
const EVENT_WORKSPACE_CHANGED = "workspace:changed";

// KNOWN_QUARANTINE_REASONS is the complete, closed set of classes
// core/sync.Reject can currently produce (see core/store/sync_repository.go
// and core/sync/worker.go's verificationClass) - a rejected operation
// never carries free-form text, so this stays exhaustive rather than
// growing an ever-widening switch.
const KNOWN_QUARANTINE_REASONS = [
  "unsupported_version",
  "verification_failed",
  "empty_verified_operation",
  "op_id_reuse",
  "apply_failed",
] as const;

/**
 * quarantineReasonMessage localizes a quarantined operation's rejection
 * class (specs/product-experience's "Actionable and safe error
 * presentation" requirement: raw internal classification codes must not
 * appear as primary UI text) instead of rendering the class string
 * verbatim, falling back to a generic message for a class this build does
 * not recognize (server/client version skew) rather than showing nothing
 * or the raw code.
 */
function quarantineReasonMessage(t: (key: string) => string, reason: string): string {
  const known = (KNOWN_QUARANTINE_REASONS as readonly string[]).includes(reason);
  return t(`sync.quarantine_reason_${known ? reason : "unknown"}`);
}

// KNOWN_DEVICE_PLATFORMS is the closed set core/transport.RegistrationRequest
// currently sends (see desktop/sync.go and core/mobileapi/service.go); an
// absent or unrecognized value degrades to "unknown platform" rather than
// showing a raw string, matching quarantineReasonMessage's pattern above.
const KNOWN_DEVICE_PLATFORMS = ["windows", "android"] as const;

function devicePlatformLabel(t: (key: string) => string, platform: string | undefined): string {
  const known = !!platform && (KNOWN_DEVICE_PLATFORMS as readonly string[]).includes(platform);
  return t(`sync.device_platform_${known ? platform : "unknown"}`);
}

// deviceLastSeenLabel renders a device's last authenticated session time
// (specs/identity-and-sharing's "last-seen time when available"): older
// server deployments and devices that have not refreshed a session since
// this field was added report no value, which reads as "Never" rather than
// a missing/blank field.
function deviceLastSeenLabel(t: (key: string) => string, lastSeenAt: string | undefined): string {
  return lastSeenAt ? new Date(lastSeenAt).toLocaleString() : t("diagnostics.never_label");
}

export interface SyncPanelProps {
  deviceId: string;
  /** Called whenever the active workspace changes (joining a shared
   * workspace, or switching between held ones), so the parent screen can
   * reload notes/notebooks/tags scoped to the newly active workspace. */
  onWorkspaceChanged?: () => void;
  /** Awaited before switching the active workspace, so an edit still
   * pending in the currently open note's editor commits to this device
   * before its content becomes unreachable under the workspace about to
   * be replaced - the same local-only flush barrier navigation and lock
   * already go through (see Shell.tsx's editorPaneRef). Never involves
   * network/transport: it only waits for the local commit. */
  onBeforeWorkspaceSwitch?: () => Promise<void>;
}

export function SyncPanel({ deviceId, onWorkspaceChanged, onBeforeWorkspaceSwitch }: SyncPanelProps) {
  const { t, errorMessage } = useI18n();
  const [status, setStatus] = useState<SyncState | null>(null);
  const [pendingCount, setPendingCount] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [url, setUrl] = useState("");
  const [invite, setInvite] = useState("");
  const [fingerprint, setFingerprint] = useState("");
  const [connectionCode, setConnectionCode] = useState("");
  const [securityMode, setSecurityMode] = useState<"pinned" | "trusted">("pinned");
  // Server setup prefers the single pasted connection code (task 7.3):
  // URL, TLS policy, fingerprint, and technical diagnostics only appear
  // once a user explicitly expands this manual/advanced branch.
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [connection, setConnection] = useState<ServerConnectionInfo | null>(null);
  const [busy, setBusy] = useState(false);
  const [diagnostics, setDiagnostics] = useState<ServerDiagnostics | null>(null);
  const [devices, setDevices] = useState<SyncDevice[]>([]);
  const [quarantine, setQuarantine] = useState<QuarantineEntry[]>([]);
  const [identityCode, setIdentityCode] = useState("");
  const [workspaces, setWorkspaces] = useState<WorkspaceSummary[]>([]);
  const [workspaceMembers, setWorkspaceMembers] = useState<Record<string, WorkspaceMember[]>>({});
  const [peerIdentityCode, setPeerIdentityCode] = useState("");
  const [grantCode, setGrantCode] = useState("");
  const [peerGrantCode, setPeerGrantCode] = useState("");
  const [copied, setCopied] = useState<string | null>(null);
  const [sharingBusy, setSharingBusy] = useState(false);
  // Guided pairing wizard state (task 7.7): "Your identity code" and the
  // resulting grant code are QR-first, with the raw opaque text hidden
  // behind these toggles rather than shown as primary-flow content (see
  // specs/identity-and-sharing's "hides key envelopes and public keys
  // from the primary flow"). Sharing/joining stage through an explicit
  // confirmation before the corresponding core call ever runs.
  const [showIdentityCode, setShowIdentityCode] = useState(false);
  const [showGrantCode, setShowGrantCode] = useState(false);
  const [shareStage, setShareStage] = useState<"input" | "confirm" | "success">("input");
  const [joinStage, setJoinStage] = useState<"input" | "confirm" | "success">("input");
  // Revoking a device or a workspace member always shows the same
  // disclosure before it runs (specs/identity-and-sharing's "Revocation
  // limitation disclosure": future access only, cannot erase already-
  // downloaded data) - see confirmRevoke below, which is the only caller
  // of revokeSyncDevice/revokeWorkspaceMember in this component.
  const [revokeTarget, setRevokeTarget] = useState<
    { kind: "device"; deviceId: string; name: string } | { kind: "member"; workspaceId: string; userId: string; name: string } | null
  >(null);

  const loadDetails = useCallback(async (nextStatus: SyncState) => {
    if (nextStatus === "local_only") {
      setDevices([]);
      setQuarantine([]);
      setDiagnostics(null);
      setWorkspaces([]);
      setWorkspaceMembers({});
      setIdentityCode("");
      return;
    }
    const [deviceRows, journalRows, identity, workspaceRows] = await Promise.all([
      listSyncDevices(),
      listSyncQuarantine(),
      exportIdentity(),
      listWorkspaces(),
    ]);
    // Treat malformed bridge results as empty collections instead of letting
    // one bad response crash the entire settings surface.
    setDevices(Array.isArray(deviceRows) ? deviceRows : []);
    setQuarantine(Array.isArray(journalRows) ? journalRows : []);
    setIdentityCode(typeof identity === "string" ? identity : "");
    setWorkspaces(Array.isArray(workspaceRows) ? workspaceRows : []);
    const owned = Array.isArray(workspaceRows)
      ? workspaceRows.filter((workspace) => workspace.role === "owner")
      : [];
    const memberRows = await Promise.all(owned.map(async (workspace) => [
      workspace.workspace_id,
      await listWorkspaceMembers(workspace.workspace_id).catch(() => []),
    ] as const));
    setWorkspaceMembers(Object.fromEntries(memberRows.map(([workspaceId, members]) => [
      workspaceId,
      Array.isArray(members) ? members : [],
    ])));
  }, []);

  const loadStatus = useCallback(() => {
    setError(null);
    syncSummary()
      .then(async (summary) => {
        setStatus(summary.state);
        setPendingCount(summary.pending_count);
        await loadDetails(summary.state);
      })
      .catch((thrown: unknown) => setError(errorMessage(unwrapError(thrown))));
  }, [errorMessage, loadDetails]);

  const loadConnection = useCallback(() => {
    syncConnectionInfo()
      .then((info) => {
        setConnection(info);
        setUrl(info.url ?? "");
        setFingerprint(info.fingerprint ?? "");
        setSecurityMode(info.security_mode === "trusted" ? "trusted" : "pinned");
      })
      .catch((thrown: unknown) => setError(errorMessage(unwrapError(thrown))));
  }, [errorMessage]);

  useEffect(() => {
    // EventSyncSummary carries no payload; it signals a re-fetch instead
    // of racing a value embedded in the event itself.
    EventsOn(EVENT_SYNC_SUMMARY, loadStatus);
    EventsOn(EVENT_WORKSPACE_CHANGED, () => {
      loadStatus();
      onWorkspaceChanged?.();
    });
    loadStatus();
    loadConnection();
    return () => { EventsOff(EVENT_SYNC_SUMMARY); EventsOff(EVENT_WORKSPACE_CHANGED); };
  }, [loadConnection, loadStatus, onWorkspaceChanged]);

  // Moves the share wizard from "input" to an explicit confirmation step
  // without granting anything yet - shareWorkspace() only ever runs from
  // handleShareWorkspace below, after the user confirms.
  function requestShareConfirmation() {
    if (!peerIdentityCode.trim()) return;
    setShareStage("confirm");
  }

  function cancelShareConfirmation() {
    setShareStage("input");
  }

  async function handleShareWorkspace() {
    setSharingBusy(true);
    setError(null);
    try {
      const code = await shareWorkspace(peerIdentityCode.trim());
      setGrantCode(code);
      setPeerIdentityCode("");
      setShareStage("success");
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
      setShareStage("input");
    } finally { setSharingBusy(false); }
  }

  function resetShareFlow() {
    setShareStage("input");
    setGrantCode("");
    setShowGrantCode(false);
  }

  function requestJoinConfirmation() {
    if (!peerGrantCode.trim()) return;
    setJoinStage("confirm");
  }

  function cancelJoinConfirmation() {
    setJoinStage("input");
  }

  async function handleAcceptWorkspaceGrant() {
    setSharingBusy(true);
    setError(null);
    try {
      await acceptWorkspaceGrant(peerGrantCode.trim());
      setPeerGrantCode("");
      setJoinStage("success");
      loadStatus();
      onWorkspaceChanged?.();
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
      setJoinStage("input");
    } finally { setSharingBusy(false); }
  }

  function resetJoinFlow() {
    setJoinStage("input");
  }

  async function handleSwitchWorkspace(workspaceId: string) {
    setSharingBusy(true);
    setError(null);
    try {
      await onBeforeWorkspaceSwitch?.();
      await setActiveWorkspace(workspaceId);
      loadStatus();
      onWorkspaceChanged?.();
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
    } finally { setSharingBusy(false); }
  }

  async function confirmRevoke() {
    if (!revokeTarget) return;
    setSharingBusy(true);
    setError(null);
    try {
      if (revokeTarget.kind === "device") {
        await revokeSyncDevice(revokeTarget.deviceId);
      } else {
        await revokeWorkspaceMember(revokeTarget.workspaceId, revokeTarget.userId);
      }
      setRevokeTarget(null);
      await loadStatus();
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
    } finally { setSharingBusy(false); }
  }

  async function handleCopy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(text);
      setTimeout(() => setCopied((current) => (current === text ? null : current)), 2000);
    } catch {
      // Clipboard access can be denied by the OS; the code remains visible
      // and selectable in its own read-only textarea as a fallback.
    }
  }

  async function performConnect(payload: Omit<ConnectServerRequest, "device_name">) {
    setBusy(true);
    setError(null);
    try {
      const info = await connectServer({ ...payload, device_name: "Windows desktop" });
      setConnection(info);
      setUrl(info.url);
      setFingerprint(info.fingerprint ?? "");
      setSecurityMode(info.security_mode);
      loadStatus();
      return true;
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
      return false;
    } finally { setBusy(false); }
  }

  // The simple path: a pasted connection code (from a QR scan or copied
  // text) carries the server URL, invite, TLS policy, and fingerprint
  // together, so it never touches the advanced manual fields below.
  async function connectWithCode() {
    const ok = await performConnect({
      url: "", invite_code: "", fingerprint: "", security_mode: "",
      qr_code: connectionCode.trim(),
    });
    if (ok) setConnectionCode("");
  }

  // The advanced path: URL, invite, TLS policy, and fingerprint are all
  // entered manually, for setups without a connection code to paste.
  async function connectManually() {
    const ok = await performConnect({
      url, invite_code: invite, fingerprint, security_mode: securityMode, qr_code: "",
    });
    if (ok) setInvite("");
  }

  async function disconnect() {
    setBusy(true);
    try {
      await disableServer();
      setConnection((current) => current ? { ...current, enabled: false } : current);
      setStatus("local_only");
      await loadDetails("local_only");
    } catch (thrown) { setError(errorMessage(unwrapError(thrown))); }
    finally { setBusy(false); }
  }

  async function runDiagnostics() {
    setBusy(true);
    try { setDiagnostics(await diagnoseServer()); }
    catch (thrown) { setError(errorMessage(unwrapError(thrown))); }
    finally { setBusy(false); }
  }

  return (
    <div className="sync-panel">
      <section aria-labelledby="sync-status-title">
        <h3 id="sync-status-title">{t("sync.status_title")}</h3>
        {error ? (
          <ErrorState message={error} onRetry={loadStatus} />
        ) : status === null ? (
          <LoadingState />
        ) : (
          <>
            <div className={`sync-status-card sync-status-${status}`}>
              <span className="sync-status-dot" aria-hidden="true" />
              <div><strong>{t(`sync.status_${status}`)}</strong><p>{t(`sync.status_${status}_description`)}</p></div>
            </div>
            {pendingCount > 0 ? (
              <p className="sync-pending-count" role="status"><strong>{t("sync.pending_count_label")}</strong> {pendingCount}</p>
            ) : null}
          </>
        )}
      </section>

      <section aria-labelledby="sync-server-title">
        <h3 id="sync-server-title">{t("sync.server_title")}</h3>
        {connection?.enabled && connection.url ? (
          <dl className="sync-connection-summary">
            <div><dt>{t("sync.current_server_label")}</dt><dd>{connection.url}</dd></div>
            <div><dt>{t("sync.protocol_label")}</dt><dd>{connection.protocol === "https" ? t("sync.protocol_https") : connection.protocol}</dd></div>
            <div><dt>{t("sync.verification_label")}</dt><dd>{t(`sync.verification_${connection.security_mode}`)}</dd></div>
          </dl>
        ) : null}
        <div className="sync-connect-form">
          <label>
            {t("sync.qr_label")}
            <textarea value={connectionCode} onChange={(event) => setConnectionCode(event.target.value)} />
          </label>
          <p className="hint">{t("sync.connection_code_hint")}</p>
          <button
            type="button"
            disabled={busy || !connectionCode.trim()}
            onClick={() => void connectWithCode()}
          >
            {t("sync.connect_code_button")}
          </button>
          {connection?.enabled ? (
            <button type="button" disabled={busy} onClick={() => void disconnect()}>
              {t("sync.disconnect_button")}
            </button>
          ) : null}
        </div>

        <div className="sync-advanced">
          <button type="button" onClick={() => setAdvancedOpen((current) => !current)} aria-expanded={advancedOpen}>
            {t("sync.advanced_setup_title")}
          </button>
          {!advancedOpen ? null : (
            <div className="sync-connect-form">
              <label>{t("sync.url_label")}<input type="url" value={url} onChange={(event) => setUrl(event.target.value)} /></label>
              <label>{t("sync.invite_label")}<input type="password" value={invite} onChange={(event) => setInvite(event.target.value)} /></label>
              <label>{t("sync.verification_label")}
                <select aria-label={t("sync.verification_label")} value={securityMode} onChange={(event) => setSecurityMode(event.target.value as "pinned" | "trusted")}>
                  <option value="pinned">{t("sync.verification_pinned")}</option>
                  <option value="trusted">{t("sync.verification_trusted")}</option>
                </select>
              </label>
              {securityMode === "pinned" ? <>
                <label>{t("sync.server_fingerprint_label")}<input value={fingerprint} onChange={(event) => setFingerprint(event.target.value)} /></label>
                <p>{t("sync.server_fingerprint_warning")}</p>
              </> : null}
              <button
                className={connection?.enabled ? "sync-connection-primary-action" : undefined}
                type="button"
                disabled={busy || !url}
                onClick={() => void connectManually()}
              >
                {connection?.enabled ? t("sync.change_server_button") : t("sync.connect_button")}
              </button>
              {connection?.enabled ? <div className="sync-connection-actions">
                <button type="button" disabled={busy} onClick={() => void runDiagnostics()}>{t("sync.diagnose_button")}</button>
                {diagnostics && <p role="status">{diagnostics.authenticated ? t("sync.diagnostics_ok") : t("sync.diagnostics_failed")} ({diagnostics.latency_ms} ms)</p>}
              </div> : null}
            </div>
          )}
        </div>
      </section>

      <section aria-labelledby="sync-journal-title">
        <h3 id="sync-journal-title">{t("sync.journal_title")}</h3>
        {quarantine.length === 0 ? <p>{t("sync.journal_empty")}</p> : quarantine.map((entry) => (
          <div className="sync-device-row" key={entry.operation_id}><div><code>{entry.operation_id}</code><p>{quarantineReasonMessage(t, entry.reason)}</p></div><button type="button" onClick={() => void retrySyncQuarantine(entry.operation_id).then(loadStatus)}>{t("common.retry")}</button></div>
        ))}
      </section>

      <section aria-labelledby="sync-devices-title">
        <h3 id="sync-devices-title">{t("sync.devices_title")}</h3>
        <div className="sync-device-row">
          <div>
            <strong>{t("sync.this_device")}</strong>
            <p>{devicePlatformLabel(t, devices.find((device) => device.device_id === deviceId)?.platform)}</p>
          </div>
          <span>{t("sync.device_local_badge")}</span>
        </div>
        {devices.filter((device) => device.device_id !== deviceId).map((device) => (
          <div className="sync-device-row" key={device.device_id}>
            <div>
              <strong>{device.display_name}</strong>
              <p>
                {devicePlatformLabel(t, device.platform)} · {t("sync.device_last_seen_label")}: {deviceLastSeenLabel(t, device.last_seen_at)}
              </p>
            </div>
            {device.revoked_at ? (
              <span>{t("sync.device_revoked_badge")}</span>
            ) : (
              <>
                <span>{t("sync.device_active_badge")}</span>
                <button type="button" onClick={() => setRevokeTarget({ kind: "device", deviceId: device.device_id, name: device.display_name })}>
                  {t("sync.revoke_device_button")}
                </button>
              </>
            )}
          </div>
        ))}
        {status === "local_only" && <p>{t("sync.devices_unavailable")}</p>}
      </section>

      {status !== "local_only" && status !== null ? (
        <section aria-labelledby="sync-workspaces-title">
          <h3 id="sync-workspaces-title">{t("sync.workspaces_title")}</h3>
          {workspaces.map((workspace) => (
            <div key={workspace.workspace_id}>
              <div className="sync-device-row">
                <div>
                  <strong>
                    {t(`sync.workspace_role_${workspace.role === "owner" || workspace.role === "member" ? workspace.role : "unknown"}`)}
                  </strong>
                  <code>{workspace.workspace_id}</code>
                  {typeof workspace.member_count === "number" && (
                    <p>{workspace.member_count} {t("sync.workspace_members_label")}</p>
                  )}
                </div>
                {workspace.active ? (
                  <span>{t("sync.workspace_active_badge")}</span>
                ) : (
                  <button type="button" disabled={sharingBusy} onClick={() => void handleSwitchWorkspace(workspace.workspace_id)}>
                    {t("sync.workspace_switch_button")}
                  </button>
                )}
              </div>
              {workspace.role === "owner" && workspaceMembers[workspace.workspace_id]?.map((member) => (
                  <div className="sync-device-row" key={member.user_id}>
                    <div>
                      <strong>{member.display_name || t("sync.workspace_client_unnamed")}</strong>
                    </div>
                    {member.role === "owner" ? (
                      <span>{t("sync.workspace_owner_badge")}</span>
                    ) : member.revoked_at ? (
                      <span>{t("sync.device_revoked_badge")}</span>
                    ) : (
                      <button
                        type="button"
                        disabled={sharingBusy}
                        onClick={() => setRevokeTarget({
                          kind: "member", workspaceId: workspace.workspace_id, userId: member.user_id,
                          name: member.display_name || t("sync.workspace_client_unnamed"),
                        })}
                      >
                        {t("sync.workspace_remove_member_button")}
                      </button>
                    )}
                  </div>
                ))}
            </div>
          ))}

          <div className="sync-connect-form">
            <h4>{t("sync.export_identity_title")}</h4>
            <p>{t("sync.export_identity_description")}</p>
            {identityCode ? <QrCode value={identityCode} alt={t("sync.qr_alt_identity")} /> : null}
            <button type="button" onClick={() => setShowIdentityCode((current) => !current)} disabled={!identityCode}>
              {showIdentityCode ? t("sync.hide_code_button") : t("sync.show_code_button")}
            </button>
            {showIdentityCode ? (
              <>
                <label>
                  {t("sync.export_identity_title")}
                  <textarea readOnly value={identityCode} onFocus={(event) => event.currentTarget.select()} />
                </label>
                <button type="button" onClick={() => void handleCopy(identityCode)} disabled={!identityCode}>
                  {copied === identityCode ? t("sync.copied_label") : t("sync.copy_button")}
                </button>
              </>
            ) : null}
          </div>

          <div className="sync-connect-form">
            <h4>{t("sync.share_workspace_title")}</h4>
            {shareStage === "success" ? (
              <>
                <p role="status">
                  <strong>{t("sync.share_success_title")}</strong> {t("sync.share_success_description")}
                </p>
                <QrCode value={grantCode} alt={t("sync.qr_alt_grant")} />
                <button type="button" onClick={() => setShowGrantCode((current) => !current)}>
                  {showGrantCode ? t("sync.hide_code_button") : t("sync.show_code_button")}
                </button>
                {showGrantCode ? (
                  <>
                    <label>
                      {t("sync.grant_code_label")}
                      <textarea readOnly value={grantCode} onFocus={(event) => event.currentTarget.select()} />
                    </label>
                    <button type="button" onClick={() => void handleCopy(grantCode)}>
                      {copied === grantCode ? t("sync.copied_label") : t("sync.copy_button")}
                    </button>
                  </>
                ) : null}
                <button type="button" onClick={resetShareFlow}>
                  {t("sync.share_another_button")}
                </button>
              </>
            ) : shareStage === "confirm" ? (
              <>
                <p role="alert">
                  <strong>{t("sync.share_confirm_title")}</strong> {t("sync.share_confirm_description")}
                </p>
                <button type="button" disabled={sharingBusy} onClick={() => void handleShareWorkspace()}>
                  {t("sync.share_confirm_button")}
                </button>
                <button type="button" disabled={sharingBusy} onClick={cancelShareConfirmation}>
                  {t("common.cancel")}
                </button>
              </>
            ) : (
              <>
                <label>
                  {t("sync.paste_identity_label")}
                  <textarea value={peerIdentityCode} onChange={(event) => setPeerIdentityCode(event.target.value)} />
                </label>
                <button type="button" disabled={sharingBusy || !peerIdentityCode} onClick={requestShareConfirmation}>
                  {t("sync.share_workspace_button")}
                </button>
              </>
            )}
          </div>

          <div className="sync-connect-form">
            <h4>{t("sync.join_workspace_title")}</h4>
            {joinStage === "success" ? (
              <>
                <p role="status">
                  <strong>{t("sync.join_success_title")}</strong> {t("sync.join_success_description")}
                </p>
                <button type="button" onClick={resetJoinFlow}>
                  {t("common.close")}
                </button>
              </>
            ) : joinStage === "confirm" ? (
              <>
                <p role="alert">
                  <strong>{t("sync.join_confirm_title")}</strong> {t("sync.join_confirm_description")}
                </p>
                <button type="button" disabled={sharingBusy} onClick={() => void handleAcceptWorkspaceGrant()}>
                  {t("sync.join_confirm_button")}
                </button>
                <button type="button" disabled={sharingBusy} onClick={cancelJoinConfirmation}>
                  {t("common.cancel")}
                </button>
              </>
            ) : (
              <>
                <label>
                  {t("sync.paste_grant_label")}
                  <textarea value={peerGrantCode} onChange={(event) => setPeerGrantCode(event.target.value)} />
                </label>
                <button type="button" disabled={sharingBusy || !peerGrantCode} onClick={requestJoinConfirmation}>
                  {t("sync.join_workspace_button")}
                </button>
              </>
            )}
          </div>
        </section>
      ) : null}

      {revokeTarget ? (
        <Modal title={`${t("sync.revoke_confirm_title")}: ${revokeTarget.name}`} onClose={() => setRevokeTarget(null)}>
          <p>{t("sync.revoke_confirm_description")}</p>
          <div className="dialog-actions">
            <button type="button" disabled={sharingBusy} onClick={() => void confirmRevoke()}>
              {sharingBusy ? t("sync.revoking_label") : t("sync.revoke_confirm_button")}
            </button>
            <button type="button" className="link-button" disabled={sharingBusy} onClick={() => setRevokeTarget(null)}>
              {t("common.cancel")}
            </button>
          </div>
          {error ? <ErrorState message={error} /> : null}
        </Modal>
      ) : null}
    </div>
  );
}

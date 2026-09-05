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
  syncError,
  syncStatus,
  type QuarantineEntry,
  type ServerConnectionInfo,
  type ServerDiagnostics,
  type SyncDevice,
  type SyncStatusValue,
  type WorkspaceSummary,
  type WorkspaceMember,
  unwrapError,
} from "../api";
import { useI18n } from "../i18n";
import { EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";

const EVENT_SYNC_STATUS = "sync:status";
const EVENT_SYNC_ERROR = "sync:error";
const EVENT_WORKSPACE_CHANGED = "workspace:changed";
const KNOWN_STATUSES: readonly SyncStatusValue[] = ["disabled", "offline", "active", "current", "failed"];

function isSyncStatus(value: unknown): value is SyncStatusValue {
  return typeof value === "string" && KNOWN_STATUSES.includes(value as SyncStatusValue);
}

export interface SyncPanelProps {
  deviceId: string;
  /** Called whenever the active workspace changes (joining a shared
   * workspace, or switching between held ones), so the parent screen can
   * reload notes/notebooks/tags scoped to the newly active workspace. */
  onWorkspaceChanged?: () => void;
}

export function SyncPanel({ deviceId, onWorkspaceChanged }: SyncPanelProps) {
  const { t, errorMessage } = useI18n();
  const [status, setStatus] = useState<SyncStatusValue | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [syncErrorDetail, setSyncErrorDetail] = useState("");
  const [url, setUrl] = useState("");
  const [invite, setInvite] = useState("");
  const [fingerprint, setFingerprint] = useState("");
  const [qr, setQr] = useState("");
  const [securityMode, setSecurityMode] = useState<"pinned" | "trusted">("pinned");
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

  const loadDetails = useCallback(async (nextStatus: SyncStatusValue) => {
    if (nextStatus === "disabled") {
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
    Promise.all([syncStatus(), syncError()])
      .then(async ([next, detail]) => { setStatus(next); setSyncErrorDetail(detail); await loadDetails(next); })
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
    EventsOn(EVENT_SYNC_STATUS, (next: unknown) => {
      if (isSyncStatus(next)) {
        setStatus(next);
        setError(null);
        void loadDetails(next).catch(() => undefined);
      } else {
        setError(errorMessage({ code: "internal", message: "unknown synchronization status" }));
      }
    });
    EventsOn(EVENT_SYNC_ERROR, (detail: unknown) => {
      if (typeof detail === "string") setSyncErrorDetail(detail);
    });
    EventsOn(EVENT_WORKSPACE_CHANGED, () => {
      loadStatus();
      onWorkspaceChanged?.();
    });
    loadStatus();
    loadConnection();
    return () => { EventsOff(EVENT_SYNC_STATUS); EventsOff(EVENT_SYNC_ERROR); EventsOff(EVENT_WORKSPACE_CHANGED); };
  }, [errorMessage, loadConnection, loadDetails, loadStatus, onWorkspaceChanged]);

  async function handleShareWorkspace() {
    setSharingBusy(true);
    setError(null);
    try {
      const code = await shareWorkspace(peerIdentityCode.trim());
      setGrantCode(code);
      setPeerIdentityCode("");
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
    } finally { setSharingBusy(false); }
  }

  async function handleAcceptWorkspaceGrant() {
    setSharingBusy(true);
    setError(null);
    try {
      await acceptWorkspaceGrant(peerGrantCode.trim());
      setPeerGrantCode("");
      loadStatus();
      onWorkspaceChanged?.();
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
    } finally { setSharingBusy(false); }
  }

  async function handleSwitchWorkspace(workspaceId: string) {
    setSharingBusy(true);
    setError(null);
    try {
      await setActiveWorkspace(workspaceId);
      loadStatus();
      onWorkspaceChanged?.();
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
    } finally { setSharingBusy(false); }
  }

  async function handleRevokeWorkspaceMember(workspaceId: string, memberUserId: string) {
    setSharingBusy(true);
    setError(null);
    try {
      await revokeWorkspaceMember(workspaceId, memberUserId);
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

  async function connect() {
    setBusy(true);
    setError(null);
    try {
      const info = await connectServer({ url, invite_code: invite, fingerprint, security_mode: securityMode, qr_code: qr, device_name: "Windows desktop" });
      setConnection(info);
      setUrl(info.url);
      setFingerprint(info.fingerprint ?? "");
      setSecurityMode(info.security_mode);
      setInvite("");
      setQr("");
      loadStatus();
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
    } finally { setBusy(false); }
  }

  async function disconnect() {
    setBusy(true);
    try {
      await disableServer();
      setConnection((current) => current ? { ...current, enabled: false } : current);
      setStatus("disabled");
      await loadDetails("disabled");
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
          <div className="sync-status-error"><p role="alert">{error}</p><button type="button" onClick={loadStatus}>{t("common.retry")}</button></div>
        ) : status === null ? <p>{t("common.loading")}</p> : (
          <><div className={`sync-status-card sync-status-${status}`}><span className="sync-status-dot" aria-hidden="true" /><div><strong>{t(`sync.status_${status}`)}</strong><p>{t(`sync.status_${status}_description`)}</p></div></div>{syncErrorDetail ? <p className="sync-error-detail" role="status"><strong>{t("sync.error_details_label")}</strong> {syncErrorDetail}</p> : null}</>
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
          <label>{t("sync.qr_label")}<textarea value={qr} onChange={(event) => setQr(event.target.value)} /></label>
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
            disabled={busy || (!qr && !url)}
            onClick={() => void connect()}
          >
            {connection?.enabled ? t("sync.change_server_button") : t("sync.connect_button")}
          </button>
          {connection?.enabled ? <div className="sync-connection-actions">
            <button type="button" disabled={busy} onClick={() => void runDiagnostics()}>{t("sync.diagnose_button")}</button>
            <button type="button" disabled={busy} onClick={() => void disconnect()}>{t("sync.disconnect_button")}</button>
            {diagnostics && <p role="status">{diagnostics.authenticated ? t("sync.diagnostics_ok") : t("sync.diagnostics_failed")} ({diagnostics.latency_ms} ms)</p>}
          </div> : null}
        </div>
      </section>

      <section aria-labelledby="sync-journal-title">
        <h3 id="sync-journal-title">{t("sync.journal_title")}</h3>
        {quarantine.length === 0 ? <p>{t("sync.journal_empty")}</p> : quarantine.map((entry) => (
          <div className="sync-device-row" key={entry.operation_id}><div><code>{entry.operation_id}</code><p>{entry.reason}</p></div><button type="button" onClick={() => void retrySyncQuarantine(entry.operation_id).then(loadStatus)}>{t("common.retry")}</button></div>
        ))}
      </section>

      <section aria-labelledby="sync-devices-title">
        <h3 id="sync-devices-title">{t("sync.devices_title")}</h3>
        <div className="sync-device-row"><div><strong>{t("sync.this_device")}</strong><code>{deviceId}</code></div><span>{t("sync.device_local_badge")}</span></div>
        {devices.filter((device) => device.device_id !== deviceId).map((device) => (
          <div className="sync-device-row" key={device.device_id}><div><strong>{device.display_name}</strong><code>{device.device_id}</code></div>{device.revoked_at ? <span>{t("sync.device_revoked_badge")}</span> : <button type="button" onClick={() => void revokeSyncDevice(device.device_id).then(loadStatus)}>{t("sync.revoke_device_button")}</button>}</div>
        ))}
        {status === "disabled" && <p>{t("sync.devices_unavailable")}</p>}
      </section>

      {status !== "disabled" && status !== null ? (
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
              {workspace.role === "owner" && workspaceMembers[workspace.workspace_id]
                ?.filter((member) => !member.revoked_at)
                .map((member) => (
                  <div className="sync-device-row" key={member.user_id}>
                    <div>
                      <strong>{member.display_name || t("sync.workspace_client_unnamed")}</strong>
                      <code>{member.user_id}</code>
                    </div>
                    {member.role === "owner" ? <span>{t("sync.workspace_owner_badge")}</span> : (
                      <button type="button" disabled={sharingBusy} onClick={() => void handleRevokeWorkspaceMember(workspace.workspace_id, member.user_id)}>
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
            <label>
              {t("sync.export_identity_title")}
              <textarea readOnly value={identityCode} onFocus={(event) => event.currentTarget.select()} />
            </label>
            <button type="button" onClick={() => void handleCopy(identityCode)} disabled={!identityCode}>
              {copied === identityCode ? t("sync.copied_label") : t("sync.copy_button")}
            </button>
          </div>

          <div className="sync-connect-form">
            <h4>{t("sync.share_workspace_title")}</h4>
            <label>
              {t("sync.paste_identity_label")}
              <textarea value={peerIdentityCode} onChange={(event) => setPeerIdentityCode(event.target.value)} />
            </label>
            <button type="button" disabled={sharingBusy || !peerIdentityCode} onClick={() => void handleShareWorkspace()}>
              {t("sync.share_workspace_button")}
            </button>
            {grantCode ? (
              <>
                <label>
                  {t("sync.grant_code_label")}
                  <textarea readOnly value={grantCode} onFocus={(event) => event.currentTarget.select()} />
                </label>
                <p>{t("sync.grant_code_description")}</p>
                <button type="button" onClick={() => void handleCopy(grantCode)}>
                  {copied === grantCode ? t("sync.copied_label") : t("sync.copy_button")}
                </button>
              </>
            ) : null}
          </div>

          <div className="sync-connect-form">
            <h4>{t("sync.join_workspace_title")}</h4>
            <label>
              {t("sync.paste_grant_label")}
              <textarea value={peerGrantCode} onChange={(event) => setPeerGrantCode(event.target.value)} />
            </label>
            <button type="button" disabled={sharingBusy || !peerGrantCode} onClick={() => void handleAcceptWorkspaceGrant()}>
              {t("sync.join_workspace_button")}
            </button>
          </div>
        </section>
      ) : null}
    </div>
  );
}

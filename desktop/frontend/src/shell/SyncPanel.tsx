import { useCallback, useEffect, useState } from "react";

import {
  connectServer,
  diagnoseServer,
  disableServer,
  listSyncDevices,
  listSyncQuarantine,
  retrySyncQuarantine,
  revokeSyncDevice,
  syncStatus,
  type QuarantineEntry,
  type ServerDiagnostics,
  type SyncDevice,
  type SyncStatusValue,
  unwrapError,
} from "../api";
import { useI18n } from "../i18n";
import { EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";

const EVENT_SYNC_STATUS = "sync:status";
const KNOWN_STATUSES: readonly SyncStatusValue[] = ["disabled", "offline", "active", "current", "failed"];

function isSyncStatus(value: unknown): value is SyncStatusValue {
  return typeof value === "string" && KNOWN_STATUSES.includes(value as SyncStatusValue);
}

export interface SyncPanelProps { deviceId: string; }

export function SyncPanel({ deviceId }: SyncPanelProps) {
  const { t, errorMessage } = useI18n();
  const [status, setStatus] = useState<SyncStatusValue | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [url, setUrl] = useState("");
  const [invite, setInvite] = useState("");
  const [fingerprint, setFingerprint] = useState("");
  const [qr, setQr] = useState("");
  const [trusted, setTrusted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [diagnostics, setDiagnostics] = useState<ServerDiagnostics | null>(null);
  const [devices, setDevices] = useState<SyncDevice[]>([]);
  const [quarantine, setQuarantine] = useState<QuarantineEntry[]>([]);

  const loadDetails = useCallback(async (nextStatus: SyncStatusValue) => {
    if (nextStatus === "disabled") {
      setDevices([]);
      setQuarantine([]);
      setDiagnostics(null);
      return;
    }
    const [deviceRows, journalRows] = await Promise.all([listSyncDevices(), listSyncQuarantine()]);
    // Treat malformed bridge results as empty collections instead of letting
    // one bad response crash the entire settings surface.
    setDevices(Array.isArray(deviceRows) ? deviceRows : []);
    setQuarantine(Array.isArray(journalRows) ? journalRows : []);
  }, []);

  const loadStatus = useCallback(() => {
    setError(null);
    syncStatus()
      .then(async (next) => { setStatus(next); await loadDetails(next); })
      .catch((thrown: unknown) => setError(errorMessage(unwrapError(thrown))));
  }, [errorMessage, loadDetails]);

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
    loadStatus();
    return () => EventsOff(EVENT_SYNC_STATUS);
  }, [errorMessage, loadDetails, loadStatus]);

  async function connect() {
    setBusy(true);
    setError(null);
    try {
      await connectServer({ url, invite_code: invite, fingerprint, security_mode: trusted ? "trusted" : "pinned", qr_code: qr, device_name: "Windows desktop" });
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
          <div className={`sync-status-card sync-status-${status}`}><span className="sync-status-dot" aria-hidden="true" /><div><strong>{t(`sync.status_${status}`)}</strong><p>{t(`sync.status_${status}_description`)}</p></div></div>
        )}
      </section>

      <section aria-labelledby="sync-server-title">
        <h3 id="sync-server-title">{t("sync.server_title")}</h3>
        {status === "disabled" || status === null ? (
          <div className="sync-connect-form">
            <label>{t("sync.qr_label")}<textarea value={qr} onChange={(event) => setQr(event.target.value)} /></label>
            <label>{t("sync.url_label")}<input type="url" value={url} onChange={(event) => setUrl(event.target.value)} /></label>
            <label>{t("sync.invite_label")}<input type="password" value={invite} onChange={(event) => setInvite(event.target.value)} /></label>
            <label>{t("sync.server_fingerprint_label")}<input value={fingerprint} onChange={(event) => setFingerprint(event.target.value)} /></label>
            <label className="sync-checkbox-label"><input type="checkbox" checked={trusted} onChange={(event) => setTrusted(event.target.checked)} />{t("sync.trusted_certificate_label")}</label>
            <p>{t("sync.server_fingerprint_warning")}</p>
            <button type="button" disabled={busy || (!qr && !url)} onClick={() => void connect()}>{t("sync.connect_button")}</button>
          </div>
        ) : (
          <div>
            <button type="button" disabled={busy} onClick={() => void runDiagnostics()}>{t("sync.diagnose_button")}</button>
            <button type="button" disabled={busy} onClick={() => void disconnect()}>{t("sync.disconnect_button")}</button>
            {diagnostics && <p role="status">{diagnostics.authenticated ? t("sync.diagnostics_ok") : t("sync.diagnostics_failed")} ({diagnostics.latency_ms} ms)</p>}
          </div>
        )}
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
    </div>
  );
}

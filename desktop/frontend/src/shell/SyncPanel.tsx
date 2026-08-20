import { useCallback, useEffect, useState } from "react";

import { syncStatus, type SyncStatusValue, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";

// Matches desktop/events.go's EventSyncStatus.
const EVENT_SYNC_STATUS = "sync:status";
const KNOWN_STATUSES: readonly SyncStatusValue[] = [
  "disabled",
  "offline",
  "active",
  "current",
  "failed",
];

function isSyncStatus(value: unknown): value is SyncStatusValue {
  return typeof value === "string" && KNOWN_STATUSES.includes(value as SyncStatusValue);
}

export interface SyncPanelProps {
  deviceId: string;
}

/**
 * SyncPanel renders only synchronization behavior that exists in the
 * current local-only phase. The current device and disabled transport are
 * real; remote devices, server controls, and quarantine records are
 * deliberately represented as unavailable/empty rather than fabricated.
 */
export function SyncPanel({ deviceId }: SyncPanelProps) {
  const { t, errorMessage } = useI18n();
  const [status, setStatus] = useState<SyncStatusValue | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadStatus = useCallback(() => {
    setError(null);
    syncStatus()
      .then(setStatus)
      .catch((thrown: unknown) => setError(errorMessage(unwrapError(thrown))));
  }, [errorMessage]);

  useEffect(() => {
    EventsOn(EVENT_SYNC_STATUS, (next: unknown) => {
      if (isSyncStatus(next)) {
        setStatus(next);
        setError(null);
      } else {
        setError(errorMessage({ code: "internal", message: "unknown synchronization status" }));
      }
    });
    loadStatus();
    return () => EventsOff(EVENT_SYNC_STATUS);
  }, [errorMessage, loadStatus]);

  return (
    <div className="sync-panel">
      <section aria-labelledby="sync-status-title">
        <h3 id="sync-status-title">{t("sync.status_title")}</h3>
        {error ? (
          <div className="sync-status-error">
            <p role="alert">{error}</p>
            <button type="button" onClick={loadStatus}>
              {t("common.retry")}
            </button>
          </div>
        ) : status === null ? (
          <p>{t("common.loading")}</p>
        ) : (
          <div className={`sync-status-card sync-status-${status}`}>
            <span className="sync-status-dot" aria-hidden="true" />
            <div>
              <strong>{t(`sync.status_${status}`)}</strong>
              <p>{t(`sync.status_${status}_description`)}</p>
            </div>
          </div>
        )}
      </section>

      <section aria-labelledby="sync-server-title">
        <h3 id="sync-server-title">{t("sync.server_title")}</h3>
        <p>{t("sync.server_unavailable")}</p>
        <button type="button" disabled>
          {t("sync.connect_button")}
        </button>
      </section>

      <section aria-labelledby="sync-journal-title">
        <h3 id="sync-journal-title">{t("sync.journal_title")}</h3>
        <p>{t("sync.journal_empty")}</p>
      </section>

      <section aria-labelledby="sync-devices-title">
        <h3 id="sync-devices-title">{t("sync.devices_title")}</h3>
        <div className="sync-device-row">
          <div>
            <strong>{t("sync.this_device")}</strong>
            <code>{deviceId}</code>
          </div>
          <span>{t("sync.device_local_badge")}</span>
        </div>
        <p>{t("sync.devices_unavailable")}</p>
      </section>
    </div>
  );
}

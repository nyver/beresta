import { useEffect, useState } from "react";

import { diagnosticSummary, unwrapError, type DiagnosticSummary } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { BackupsPanel } from "./BackupsPanel";
import { DataCheckPanel } from "./DataCheckPanel";
import { DiagnosticsPanel } from "./DiagnosticsPanel";
import { ImportExportPanel } from "./ImportExportPanel";
import { ShellIntegrationPanel } from "./ShellIntegrationPanel";
import { ErrorState } from "./StatusState";
import { SyncPanel } from "./SyncPanel";

/**
 * The stable cross-platform information architecture (specs/product-
 * experience's "Stable cross-platform information architecture"
 * requirement, task 7.9): every settings surface lives under one of these
 * six groups on both Windows and Android, with platform-specific options
 * nested under the matching group rather than scattered across top-level
 * controls.
 */
export type SettingsGroup = "general" | "security" | "synchronization" | "data" | "advanced" | "about";

export const SETTINGS_GROUPS: readonly SettingsGroup[] = [
  "general",
  "security",
  "synchronization",
  "data",
  "advanced",
  "about",
];

export interface SettingsPanelProps {
  activeGroup: SettingsGroup;
  onGroupChange: (group: SettingsGroup) => void;
  account: main.AccountInfo;
  autoLockMinutes: number | null;
  onAutoLockChange: (minutes: number) => void;
  onRestored: () => void;
  onImported: () => void;
  onWorkspaceChanged?: () => void;
  onBeforeWorkspaceSwitch?: () => Promise<void>;
}

/**
 * AboutPanel shows the product identity, current version, and Beresta's
 * privacy defaults (specs/product-experience's "Privacy-preserving product
 * defaults" requirement) without exposing any other diagnostic detail -
 * the fuller technical/user diagnostics view stays under Advanced.
 */
function AboutPanel() {
  const { t, errorMessage } = useI18n();
  const [summary, setSummary] = useState<DiagnosticSummary | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let canceled = false;
    diagnosticSummary()
      .then((loaded) => {
        if (!canceled) setSummary(loaded);
      })
      .catch((thrown: unknown) => {
        if (!canceled) setError(errorMessage(unwrapError(thrown)));
      });
    return () => {
      canceled = true;
    };
  }, [errorMessage]);

  return (
    <section className="about-panel" aria-label={t("settings.group_about")}>
      {/* The product name is a brand, not translated content. */}
      <h3>Beresta</h3>
      <p>{t("app.tagline")}</p>
      <p>{t("about.privacy_defaults")}</p>
      {summary ? (
        <dl className="about-version">
          <div>
            <dt>{t("diagnostics.app_version_label")}</dt>
            <dd>{summary.app_version}</dd>
          </div>
          <div>
            <dt>{t("diagnostics.platform_label")}</dt>
            <dd>{summary.platform}</dd>
          </div>
        </dl>
      ) : error ? (
        <ErrorState message={error} />
      ) : null}
    </section>
  );
}

/**
 * SettingsPanel groups every settings surface into General, Security,
 * Synchronization, Data, Advanced, and About (task 7.9). Only the active
 * group's content mounts, so switching tabs never forces an unrelated
 * group's network calls (matching DiagnosticsPanel's own lazy-load
 * convention above it).
 */
export function SettingsPanel({
  activeGroup,
  onGroupChange,
  account,
  autoLockMinutes,
  onAutoLockChange,
  onRestored,
  onImported,
  onWorkspaceChanged,
  onBeforeWorkspaceSwitch,
}: SettingsPanelProps) {
  const { t } = useI18n();

  return (
    <div className="settings-panel">
      <div className="settings-group-tabs" role="tablist" aria-label={t("settings.title")}>
        {SETTINGS_GROUPS.map((group) => (
          <button
            key={group}
            type="button"
            role="tab"
            aria-selected={group === activeGroup}
            className={`settings-group-tab${group === activeGroup ? " selected" : ""}`}
            onClick={() => onGroupChange(group)}
          >
            {t(`settings.group_${group}`)}
          </button>
        ))}
      </div>

      <div className="settings-group-content" role="tabpanel">
        {activeGroup === "general" ? <ShellIntegrationPanel /> : null}

        {activeGroup === "security" ? (
          <section className="settings-security-panel">
            <label className="auto-lock-control">
              <span>{t("shell.auto_lock_label")}</span>
              <select
                value={autoLockMinutes ?? ""}
                disabled={autoLockMinutes === null}
                onChange={(event) => onAutoLockChange(Number(event.target.value))}
              >
                <option value={0}>{t("shell.auto_lock_never")}</option>
                <option value={5}>{t("shell.auto_lock_5min")}</option>
                <option value={15}>{t("shell.auto_lock_15min")}</option>
                <option value={30}>{t("shell.auto_lock_30min")}</option>
                <option value={60}>{t("shell.auto_lock_60min")}</option>
              </select>
            </label>
            {account.key_protection ? (
              <p className="key-protection-hint">
                {account.key_protection === "windows-hello"
                  ? t("shell.key_protection_hello")
                  : t("shell.key_protection_dpapi")}
              </p>
            ) : null}
          </section>
        ) : null}

        {activeGroup === "synchronization" ? (
          <SyncPanel
            deviceId={account.device_id}
            onWorkspaceChanged={onWorkspaceChanged}
            onBeforeWorkspaceSwitch={onBeforeWorkspaceSwitch}
          />
        ) : null}

        {activeGroup === "data" ? (
          <>
            <BackupsPanel onRestored={onRestored} />
            <ImportExportPanel onImported={onImported} />
          </>
        ) : null}

        {activeGroup === "advanced" ? (
          <>
            <DiagnosticsPanel />
            <DataCheckPanel />
          </>
        ) : null}

        {activeGroup === "about" ? <AboutPanel /> : null}
      </div>
    </div>
  );
}

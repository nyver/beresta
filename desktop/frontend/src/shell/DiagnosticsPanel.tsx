import { useState } from "react";

import {
  copyDiagnostics,
  diagnosticSummary,
  technicalDiagnostics,
  unwrapError,
  type DiagnosticSummary,
  type TechnicalDiagnostics,
} from "../api";
import { formatBytes, formatClockTime } from "../format";
import { useI18n } from "../i18n";

/**
 * DiagnosticsPanel covers task 4.2's user diagnostics screen: the always-
 * shown summary, plus an expandable technical-details layer and a shared
 * copy-diagnostics flow (specs/product-experience's "Layered privacy-
 * preserving diagnostics" requirement). Both layers load lazily, on first
 * expand, so opening Settings for an unrelated reason never fetches
 * diagnostics the user did not ask for.
 */
export function DiagnosticsPanel() {
  const { t, errorMessage } = useI18n();

  const [expanded, setExpanded] = useState(false);
  const [summary, setSummary] = useState<DiagnosticSummary | null>(null);
  const [summaryError, setSummaryError] = useState<string | null>(null);
  const [loadingSummary, setLoadingSummary] = useState(false);

  const [technicalExpanded, setTechnicalExpanded] = useState(false);
  const [technical, setTechnical] = useState<TechnicalDiagnostics | null>(null);
  const [technicalError, setTechnicalError] = useState<string | null>(null);
  const [loadingTechnical, setLoadingTechnical] = useState(false);

  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState<string | null>(null);

  async function loadSummary() {
    setLoadingSummary(true);
    setSummaryError(null);
    try {
      setSummary(await diagnosticSummary());
    } catch (thrown) {
      setSummaryError(errorMessage(unwrapError(thrown)));
    } finally {
      setLoadingSummary(false);
    }
  }

  function toggleExpanded() {
    const next = !expanded;
    setExpanded(next);
    if (next && !summary && !loadingSummary) {
      void loadSummary();
    }
  }

  async function loadTechnical() {
    setLoadingTechnical(true);
    setTechnicalError(null);
    try {
      setTechnical(await technicalDiagnostics());
    } catch (thrown) {
      setTechnicalError(errorMessage(unwrapError(thrown)));
    } finally {
      setLoadingTechnical(false);
    }
  }

  function toggleTechnical() {
    const next = !technicalExpanded;
    setTechnicalExpanded(next);
    if (next && !technical && !loadingTechnical) {
      void loadTechnical();
    }
  }

  async function handleCopy() {
    setCopyError(null);
    try {
      const bundle = await copyDiagnostics();
      await navigator.clipboard.writeText(bundle);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (thrown) {
      setCopyError(errorMessage(unwrapError(thrown)));
    }
  }

  return (
    <section aria-labelledby="diagnostics-title">
      <h4 id="diagnostics-title">
        <button type="button" onClick={toggleExpanded} aria-expanded={expanded}>
          {t("diagnostics.title")}
        </button>
      </h4>
      {!expanded ? null : loadingSummary ? (
        <p>{t("common.loading")}</p>
      ) : summaryError ? (
        <div>
          <p role="alert">{summaryError}</p>
          <button type="button" onClick={() => void loadSummary()}>
            {t("common.retry")}
          </button>
        </div>
      ) : summary ? (
        <>
          <dl className="diagnostics-summary">
            <div>
              <dt>{t("diagnostics.app_version_label")}</dt>
              <dd>{summary.app_version}</dd>
            </div>
            <div>
              <dt>{t("diagnostics.platform_label")}</dt>
              <dd>{summary.platform}</dd>
            </div>
            <div>
              <dt>{t("diagnostics.sync_configured_label")}</dt>
              <dd>{summary.sync_configured ? t("diagnostics.yes") : t("diagnostics.no")}</dd>
            </div>
            <div>
              <dt>{t("diagnostics.last_successful_sync_label")}</dt>
              <dd>
                {summary.last_successful_sync_unix_ms
                  ? formatClockTime(summary.last_successful_sync_unix_ms)
                  : t("diagnostics.never_label")}
              </dd>
            </div>
            <div>
              <dt>{t("diagnostics.pending_count_label")}</dt>
              <dd>{summary.pending_count}</dd>
            </div>
            <div>
              <dt>{t("diagnostics.connection_state_label")}</dt>
              <dd>{t(`sync.status_${summary.connection_state}`)}</dd>
            </div>
            <div>
              <dt>{t("diagnostics.backup_title")}</dt>
              <dd>{t(`backups.health_${summary.backup.health}`)}</dd>
            </div>
            <div>
              <dt>{t("diagnostics.storage_usage_label")}</dt>
              <dd>{formatBytes(summary.storage_usage_bytes)}</dd>
            </div>
            <div>
              <dt>{t("diagnostics.database_label")}</dt>
              <dd>{t(`diagnostics.database_${summary.database}`)}</dd>
            </div>
            <div>
              <dt>{t("diagnostics.update_label")}</dt>
              <dd>{t(`diagnostics.update_${summary.update}`)}</dd>
            </div>
          </dl>

          <button type="button" onClick={toggleTechnical} aria-expanded={technicalExpanded}>
            {t(technicalExpanded ? "diagnostics.hide_technical_details_button" : "diagnostics.show_technical_details_button")}
          </button>
          {!technicalExpanded ? null : loadingTechnical ? (
            <p>{t("common.loading")}</p>
          ) : technicalError ? (
            <div>
              <p role="alert">{technicalError}</p>
              <button type="button" onClick={() => void loadTechnical()}>
                {t("common.retry")}
              </button>
            </div>
          ) : technical ? (
            <dl className="diagnostics-technical">
              <div>
                <dt>{t("diagnostics.workspace_id_label")}</dt>
                <dd>
                  <code>{technical.workspace_id}</code>
                </dd>
              </div>
              <div>
                <dt>{t("diagnostics.device_id_label")}</dt>
                <dd>
                  <code>{technical.device_id}</code>
                </dd>
              </div>
              <div>
                <dt>{t("diagnostics.last_error_class_label")}</dt>
                <dd>{technical.last_error_class || t("diagnostics.none_label")}</dd>
              </div>
              <div>
                <dt>{t("diagnostics.pending_operation_count_label")}</dt>
                <dd>{technical.pending_operation_count}</dd>
              </div>
              <div>
                <dt>{t("diagnostics.quarantined_operation_ids_label")}</dt>
                <dd>{technical.quarantined_operation_ids.length ? technical.quarantined_operation_ids.join(", ") : t("diagnostics.none_label")}</dd>
              </div>
              <div>
                <dt>{t("diagnostics.cursor_label")}</dt>
                <dd>
                  {technical.cursor_sequence}@{technical.cursor_epoch}
                </dd>
              </div>
              <div>
                <dt>{t("diagnostics.retry_count_label")}</dt>
                <dd>{technical.retry_count}</dd>
              </div>
              <div>
                <dt>{t("diagnostics.retry_in_label")}</dt>
                <dd>{technical.retry_in_ms > 0 ? `${Math.ceil(technical.retry_in_ms / 1000)}s` : t("diagnostics.none_label")}</dd>
              </div>
              <div>
                <dt>{t("diagnostics.transport_protocol_label")}</dt>
                <dd>{technical.transport_protocol || t("diagnostics.none_label")}</dd>
              </div>
              <div>
                <dt>{t("diagnostics.transport_security_mode_label")}</dt>
                <dd>{technical.transport_security_mode || t("diagnostics.none_label")}</dd>
              </div>
              <div>
                <dt>{t("diagnostics.transport_url_label")}</dt>
                <dd>{technical.transport_url || t("diagnostics.none_label")}</dd>
              </div>
              <div>
                <dt>{t("diagnostics.migration_version_label")}</dt>
                <dd>{technical.migration_version}</dd>
              </div>
              <div>
                <dt>{t("diagnostics.rotation_pending_label")}</dt>
                <dd>{technical.rotation_pending ? t("diagnostics.yes") : t("diagnostics.no")}</dd>
              </div>
            </dl>
          ) : null}

          {copyError ? <p role="alert">{copyError}</p> : null}
          <button type="button" onClick={() => void handleCopy()}>
            {copied ? t("diagnostics.copied_label") : t("diagnostics.copy_button")}
          </button>
        </>
      ) : null}
    </section>
  );
}

import { useState } from "react";

import { runDataCheck, unwrapError, type DataCheckReport } from "../api";
import { useI18n } from "../i18n";
import { ErrorState, LoadingState } from "./StatusState";

/**
 * DataCheckPanel covers task 7.10's Advanced "Check my data" action: one
 * button that runs the single, consolidated, safe verification pass over
 * local database integrity, search index consistency, and backup health,
 * and reports either a healthy result or one actionable summary - never
 * the individual internal maintenance jobs it checks, per specs/product-
 * experience's "Routine maintenance and user data check" requirement.
 */
export function DataCheckPanel() {
  const { t, errorMessage } = useI18n();

  const [running, setRunning] = useState(false);
  const [report, setReport] = useState<DataCheckReport | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function handleRunCheck() {
    setRunning(true);
    setError(null);
    try {
      setReport(await runDataCheck());
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
    } finally {
      setRunning(false);
    }
  }

  return (
    <section aria-label={t("data_check.action_button")}>
      <button type="button" onClick={() => void handleRunCheck()} disabled={running}>
        {t("data_check.action_button")}
      </button>
      {running ? (
        <LoadingState />
      ) : error ? (
        <ErrorState message={error} />
      ) : report ? (
        <p role={report.healthy ? "status" : "alert"}>{t(`data_check.issue_${report.issue}`)}</p>
      ) : null}
    </section>
  );
}

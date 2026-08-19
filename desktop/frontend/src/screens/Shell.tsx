import { useState } from "react";

import { lockAccount } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface ShellProps {
  account: main.AccountInfo;
  onLocked: () => void;
}

/**
 * Shell is a deliberately minimal placeholder for the unlocked state: the
 * real notebook tree / note list / editor is task 5.3 onward. It exists so
 * the onboarding and unlock flows have somewhere to land and a way back to
 * a locked state to test against.
 */
export function Shell({ account, onLocked }: ShellProps) {
  const { t } = useI18n();
  const [locking, setLocking] = useState(false);

  async function handleLock() {
    setLocking(true);
    try {
      await lockAccount();
      onLocked();
    } finally {
      setLocking(false);
    }
  }

  return (
    <div className="screen shell">
      <h1>{t("shell.title")}</h1>
      <p>{t("shell.placeholder")}</p>
      <dl className="account-info">
        <dt>{t("shell.workspace_label")}</dt>
        <dd>{account.workspace_id}</dd>
      </dl>
      <button type="button" onClick={() => void handleLock()} disabled={locking}>
        {t("shell.lock_button")}
      </button>
    </div>
  );
}

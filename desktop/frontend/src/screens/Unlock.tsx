import { useState, type FormEvent } from "react";

import { unlockAccount, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface UnlockProps {
  databasePath: string;
  onAccountReady: (account: main.AccountInfo) => void;
  onSwitchToOnboarding: () => void;
}

export function Unlock({ databasePath, onAccountReady, onSwitchToOnboarding }: UnlockProps) {
  const { t, errorMessage } = useI18n();
  const [passphrase, setPassphrase] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const account = await unlockAccount({ database_path: databasePath, passphrase });
      onAccountReady(account);
    } catch (thrown) {
      setError(errorMessage(unwrapError(thrown)));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="screen unlock">
      <h1>{t("unlock.title")}</h1>
      <p className="tagline">{t("unlock.description")}</p>

      <form className="unlock-form" onSubmit={(event) => void handleSubmit(event)}>
        <label>
          {t("onboarding.passphrase_label")}
          <input
            type="password"
            value={passphrase}
            onChange={(event) => setPassphrase(event.target.value)}
            autoComplete="current-password"
            autoFocus
            required
          />
        </label>

        {error ? (
          <p className="error" role="alert">
            {error}
          </p>
        ) : null}

        <button type="submit" disabled={submitting}>
          {submitting ? t("unlock.unlocking_button") : t("unlock.button")}
        </button>

        <button type="button" className="link-button" onClick={onSwitchToOnboarding}>
          {t("unlock.switch_to_onboarding")}
        </button>
      </form>
    </div>
  );
}

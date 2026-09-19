import { useEffect, useState, type FormEvent } from "react";

import { createAccount, defaultDatabasePath, pickDatabaseDestination, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { PasswordField } from "./PasswordField";

type Mode = "local" | "server";

const MIN_PASSPHRASE_LENGTH = 8;

export interface OnboardingProps {
  /** Called once the local account is durably created and the user has
   * resolved the optional post-create sync prompt (connected or skipped).
   * openSync tells the caller whether to open the sync connect UI
   * immediately after entering the shell. */
  onAccountReady: (account: main.AccountInfo, openSync: boolean) => void;
  onSwitchToUnlock: (databasePath: string) => void;
}

export function Onboarding({ onAccountReady, onSwitchToUnlock }: OnboardingProps) {
  const { t, locale, setLocale, errorMessage } = useI18n();

  const [mode, setMode] = useState<Mode>("local");
  const [databasePath, setDatabasePath] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [confirmPassphrase, setConfirmPassphrase] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Set once CreateAccount durably succeeds: from then on this screen shows
  // the optional post-create sync prompt instead of the creation form
  // (task 7.1), rather than entering the shell immediately - "local
  // encrypted account creation SHALL not be blocked" by sync setup, but the
  // reverse also holds: sync setup should not be skipped silently either.
  const [createdAccount, setCreatedAccount] = useState<main.AccountInfo | null>(null);

  useEffect(() => {
    let canceled = false;
    defaultDatabasePath()
      .then((path) => {
        if (canceled) return;
        // Functional update so a path the user already typed or picked
        // while this call was in flight is never clobbered by the
        // default resolving late. The `?? ""` guard keeps the <input>
        // controlled even if no default could be resolved.
        setDatabasePath((current) => (current === "" ? (path ?? "") : current));
      })
      .catch(() => {
        // No default is available; the user can still type or pick a path.
      });
    return () => {
      canceled = true;
    };
  }, []);

  async function handleChooseLocation() {
    const chosen = await pickDatabaseDestination("beresta.db").catch(() => "");
    if (chosen) setDatabasePath(chosen);
  }

  async function submitCreate() {
    setError(null);

    if (passphrase.length < MIN_PASSPHRASE_LENGTH) {
      setError(t("onboarding.passphrase_too_short"));
      return;
    }
    if (passphrase !== confirmPassphrase) {
      setError(t("onboarding.passphrase_mismatch"));
      return;
    }

    setSubmitting(true);
    try {
      const account = await createAccount({
        database_path: databasePath,
        passphrase,
      });
      setCreatedAccount(account);
    } catch (thrown) {
      const apiError = unwrapError(thrown);
      if (apiError.code === "account_exists") {
        onSwitchToUnlock(databasePath);
        return;
      }
      setError(errorMessage(apiError));
    } finally {
      setSubmitting(false);
    }
  }

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    void submitCreate();
  }

  if (createdAccount) {
    return (
      <div className="screen onboarding">
        <header className="onboarding-header">
          <h1>{t("onboarding.sync_prompt_title")}</h1>
        </header>
        <p>{t("onboarding.sync_prompt_description")}</p>
        <div className="onboarding-sync-prompt-actions">
          <button type="button" onClick={() => onAccountReady(createdAccount, true)}>
            {t("onboarding.sync_prompt_connect_button")}
          </button>
          <button type="button" className="link-button" onClick={() => onAccountReady(createdAccount, false)}>
            {t("onboarding.sync_prompt_skip_button")}
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="screen onboarding">
      <header className="onboarding-header">
        <h1>{t("onboarding.title")}</h1>
        <p className="tagline">{t("app.tagline")}</p>
        <label className="language-switch">
          {t("onboarding.language_label")}
          <select
            value={locale}
            onChange={(event) => void setLocale(event.target.value as "en" | "ru")}
          >
            <option value="en">English</option>
            <option value="ru">Русский</option>
          </select>
        </label>
      </header>

      <div className="mode-cards" role="radiogroup" aria-label={t("onboarding.title")}>
        <button
          type="button"
          className={`mode-card${mode === "local" ? " selected" : ""}`}
          role="radio"
          aria-checked={mode === "local"}
          onClick={() => setMode("local")}
        >
          <strong>{t("onboarding.mode_local_title")}</strong>
          <span>{t("onboarding.mode_local_description")}</span>
        </button>
        <button
          type="button"
          className={`mode-card${mode === "server" ? " selected" : ""}`}
          role="radio"
          aria-checked={mode === "server"}
          onClick={() => setMode("server")}
        >
          <strong>{t("onboarding.mode_server_title")}</strong>
          <span>{t("onboarding.mode_server_description")}</span>
        </button>
      </div>

      {/* Neither mode card gates this form: account creation is always
       * local-first (task 7.1), so selecting "Connect to server" only
       * changes the framing text above, not what happens next - the
       * optional sync prompt appears after creation either way. */}
      <form className="onboarding-form" onSubmit={handleSubmit}>
        <label>
          {t("onboarding.database_path_label")}
          <div className="path-row">
            <input
              type="text"
              value={databasePath}
              onChange={(event) => setDatabasePath(event.target.value)}
              required
            />
            <button type="button" onClick={() => void handleChooseLocation()}>
              {t("onboarding.choose_location_button")}
            </button>
          </div>
        </label>

        <label>
          {t("onboarding.passphrase_label")}
          <PasswordField
            value={passphrase}
            onChange={(event) => setPassphrase(event.target.value)}
            onEnter={() => void submitCreate()}
            autoComplete="new-password"
            required
          />
        </label>

        <label>
          {t("onboarding.passphrase_confirm_label")}
          <PasswordField
            value={confirmPassphrase}
            onChange={(event) => setConfirmPassphrase(event.target.value)}
            onEnter={() => void submitCreate()}
            autoComplete="new-password"
            required
          />
        </label>

        <p className="hint recovery-warning">{t("onboarding.passphrase_hint")}</p>

        {error ? (
          <p className="error" role="alert">
            {error}
          </p>
        ) : null}

        <button type="submit" disabled={submitting}>
          {submitting ? t("onboarding.creating_button") : t("onboarding.create_button")}
        </button>

        <button
          type="button"
          className="link-button"
          onClick={() => onSwitchToUnlock(databasePath)}
        >
          {t("onboarding.switch_to_unlock")}
        </button>
      </form>
    </div>
  );
}

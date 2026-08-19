import { useEffect, useState, type FormEvent } from "react";

import { createAccount, defaultDatabasePath, pickDatabaseDestination, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

type Mode = "local" | "server";

const MIN_PASSPHRASE_LENGTH = 8;

export interface OnboardingProps {
  onAccountReady: (account: main.AccountInfo) => void;
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

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
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
      onAccountReady(account);
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

      {mode === "server" ? (
        <div className="server-deferred">
          <p>{t("onboarding.mode_server_description")}</p>
          <button type="button" onClick={() => setMode("local")}>
            {t("onboarding.mode_local_title")}
          </button>
        </div>
      ) : (
        <form className="onboarding-form" onSubmit={(event) => void handleSubmit(event)}>
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
            <input
              type="password"
              value={passphrase}
              onChange={(event) => setPassphrase(event.target.value)}
              autoComplete="new-password"
              required
            />
          </label>

          <label>
            {t("onboarding.passphrase_confirm_label")}
            <input
              type="password"
              value={confirmPassphrase}
              onChange={(event) => setConfirmPassphrase(event.target.value)}
              autoComplete="new-password"
              required
            />
          </label>

          <p className="hint">{t("onboarding.passphrase_hint")}</p>

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
      )}
    </div>
  );
}

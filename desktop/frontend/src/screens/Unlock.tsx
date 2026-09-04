import { useState, type FormEvent, type KeyboardEvent } from "react";

import { unlockAccount, unwrapError, wipeLocalAccount } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface UnlockProps {
  databasePath: string;
  onAccountReady: (account: main.AccountInfo) => void;
  onSwitchToOnboarding: () => void;
}

/** Typed verbatim (case-insensitive, kept in Latin script regardless of
 * locale - see the wipe confirmation label below) to enable the
 * irreversible local-wipe action, matching the windows-desktop-client
 * spec's "clear irreversible confirmation policy". */
const WIPE_CONFIRM_PHRASE = "ERASE";

export function Unlock({ databasePath, onAccountReady, onSwitchToOnboarding }: UnlockProps) {
  const { t, errorMessage } = useI18n();
  const [passphrase, setPassphrase] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [confirmingWipe, setConfirmingWipe] = useState(false);
  const [wipeConfirmText, setWipeConfirmText] = useState("");
  const [wiping, setWiping] = useState(false);
  const [wipeError, setWipeError] = useState<string | null>(null);

  // Unlock and wipe both act on the same databasePath - one opens a
  // SQLCipher connection to it, the other deletes it out from under any
  // open connection - so this screen never lets both run at once: either
  // could otherwise start while the other is still in flight (WipeLocalAccount
  // requires no open *Account for databasePath, and interleaving with a
  // just-opened UnlockAccount would race the deletion against it).
  const busy = submitting || wiping;

  async function submitUnlock() {
    if (busy) return;
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

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void submitUnlock();
  }

  function handlePassphraseKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key !== "Enter" || event.nativeEvent.isComposing || !passphrase) return;

    // Wails' WebView does not consistently translate Enter in this input into
    // a form submit, so dispatch the same action as the Unlock button here.
    event.preventDefault();
    void submitUnlock();
  }

  async function handleWipe() {
    if (busy) return;
    setWiping(true);
    setWipeError(null);
    try {
      await wipeLocalAccount(databasePath);
      onSwitchToOnboarding();
    } catch (thrown) {
      setWipeError(errorMessage(unwrapError(thrown)));
    } finally {
      setWiping(false);
    }
  }

  return (
    <div className="screen unlock">
      <h1>{t("unlock.title")}</h1>
      <p className="tagline">{t("unlock.description")}</p>

      <form className="unlock-form" onSubmit={handleSubmit}>
        <label>
          {t("onboarding.passphrase_label")}
          <input
            type="password"
            value={passphrase}
            onChange={(event) => setPassphrase(event.target.value)}
            onKeyDown={handlePassphraseKeyDown}
            autoComplete="current-password"
            autoFocus
            required
            disabled={busy}
          />
        </label>

        {error ? (
          <p className="error" role="alert">
            {error}
          </p>
        ) : null}

        <button type="submit" disabled={busy}>
          {submitting ? t("unlock.unlocking_button") : t("unlock.button")}
        </button>

        <button type="button" className="link-button" onClick={onSwitchToOnboarding}>
          {t("unlock.switch_to_onboarding")}
        </button>
      </form>

      <div className="wipe-section">
        {confirmingWipe ? (
          <div className="wipe-confirm">
            <p className="error">{t("unlock.wipe_warning")}</p>
            <label>
              {t("unlock.wipe_confirm_instructions")}
              <input
                type="text"
                value={wipeConfirmText}
                onChange={(event) => setWipeConfirmText(event.target.value)}
                autoComplete="off"
              />
            </label>
            {wipeError ? (
              <p className="error" role="alert">
                {wipeError}
              </p>
            ) : null}
            <button
              type="button"
              disabled={busy || wipeConfirmText.trim().toUpperCase() !== WIPE_CONFIRM_PHRASE}
              onClick={() => void handleWipe()}
            >
              {wiping ? t("unlock.wiping_button") : t("unlock.wipe_confirm_button")}
            </button>
            <button
              type="button"
              className="link-button"
              disabled={busy}
              onClick={() => {
                setConfirmingWipe(false);
                setWipeConfirmText("");
                setWipeError(null);
              }}
            >
              {t("common.cancel")}
            </button>
          </div>
        ) : (
          <button type="button" className="link-button" disabled={busy} onClick={() => setConfirmingWipe(true)}>
            {t("unlock.wipe_start_link")}
          </button>
        )}
      </div>
    </div>
  );
}

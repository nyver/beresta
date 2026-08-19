import { useEffect, useState } from "react";

import { autostartStatus, getSettings, unwrapError, updateSettings } from "../api";
import { useI18n } from "../i18n";

/**
 * ShellIntegrationPanel covers task 5.9's user-facing controls: the
 * global quick-note hotkey accelerator and the opt-in "launch at sign-in"
 * autostart toggle. The tray icon and context menu themselves have no
 * settings of their own - they run whenever the desktop app does.
 */
export function ShellIntegrationPanel() {
  const { t, errorMessage, ready } = useI18n();

  const [hotkey, setHotkey] = useState("");
  const [savingHotkey, setSavingHotkey] = useState(false);
  const [hotkeyError, setHotkeyError] = useState<string | null>(null);

  const [autostartEnabled, setAutostartEnabled] = useState(false);
  const [savingAutostart, setSavingAutostart] = useState(false);
  const [autostartError, setAutostartError] = useState<string | null>(null);
  const [conflictPath, setConflictPath] = useState("");

  useEffect(() => {
    if (!ready) return;
    getSettings()
      .then((settings) => {
        setHotkey(settings.quick_note_hotkey);
        setAutostartEnabled(settings.autostart_enabled);
      })
      .catch((thrown: unknown) => setHotkeyError(errorMessage(unwrapError(thrown))));
    autostartStatus()
      .then((status) => setConflictPath(status.conflict_path))
      .catch(() => {
        // Best-effort: the conflict warning is a courtesy, not something
        // worth blocking or erroring this panel over.
      });
  }, [ready, errorMessage]);

  async function handleSaveHotkey() {
    setSavingHotkey(true);
    setHotkeyError(null);
    try {
      const current = await getSettings();
      const updated = await updateSettings({ ...current, quick_note_hotkey: hotkey.trim() });
      setHotkey(updated.quick_note_hotkey);
    } catch (thrown: unknown) {
      setHotkeyError(errorMessage(unwrapError(thrown)));
    } finally {
      setSavingHotkey(false);
    }
  }

  async function handleToggleAutostart(next: boolean) {
    setSavingAutostart(true);
    setAutostartError(null);
    try {
      const current = await getSettings();
      const updated = await updateSettings({ ...current, autostart_enabled: next });
      setAutostartEnabled(updated.autostart_enabled);
      const status = await autostartStatus();
      setConflictPath(status.conflict_path);
    } catch (thrown: unknown) {
      setAutostartError(errorMessage(unwrapError(thrown)));
    } finally {
      setSavingAutostart(false);
    }
  }

  return (
    <section className="shell-integration-panel">
      <h3>{t("shellintegration.title")}</h3>

      <label className="quick-note-hotkey-control">
        <span>{t("shellintegration.hotkey_label")}</span>
        <input
          type="text"
          value={hotkey}
          onChange={(event) => setHotkey(event.target.value)}
          placeholder={t("shellintegration.hotkey_placeholder")}
        />
      </label>
      <button type="button" disabled={savingHotkey} onClick={() => void handleSaveHotkey()}>
        {savingHotkey ? t("shellintegration.saving_hotkey_button") : t("shellintegration.save_hotkey_button")}
      </button>
      {hotkeyError ? (
        <p className="error" role="alert">
          {hotkeyError}
        </p>
      ) : null}

      <label className="autostart-control">
        <input
          type="checkbox"
          checked={autostartEnabled}
          disabled={savingAutostart}
          onChange={(event) => void handleToggleAutostart(event.target.checked)}
        />
        <span>{t("shellintegration.autostart_label")}</span>
      </label>
      {autostartError ? (
        <p className="error" role="alert">
          {autostartError}
        </p>
      ) : null}
      {conflictPath ? <p className="autostart-conflict">{t("shellintegration.autostart_conflict")}</p> : null}
    </section>
  );
}

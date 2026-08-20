import { lazy, Suspense, useEffect, useState } from "react";

import { accountStatus, getSettings, unwrapError } from "./api";
import { I18nProvider, useI18n } from "./i18n";
import { Onboarding } from "./screens/Onboarding";
import { Unlock } from "./screens/Unlock";
import { main } from "../wailsjs/go/models";

// The rich editor and complete note shell pull in Quill/Yjs. Locked startup
// cannot render them before the user unlocks, so keep that code out of the
// onboarding/unlock bundle and the measured desktop cold-start path.
const Shell = lazy(async () => ({ default: (await import("./screens/Shell")).Shell }));

type Screen =
  | { name: "loading" }
  | { name: "onboarding" }
  | { name: "unlock"; databasePath: string }
  | { name: "shell"; account: main.AccountInfo }
  | { name: "error"; message: string };

export function App() {
  return (
    <I18nProvider>
      <AppShell />
    </I18nProvider>
  );
}

function AppShell() {
  const { t, errorMessage, ready } = useI18n();
  const [screen, setScreen] = useState<Screen>({ name: "loading" });

  function load() {
    let canceled = false;
    resolveInitialScreen()
      .then((next) => {
        if (!canceled) setScreen(next);
      })
      .catch((thrown: unknown) => {
        // Unlike a normal "no account yet" outcome (which
        // resolveInitialScreen itself turns into the onboarding screen,
        // not a rejection), a rejection here means Status()/GetSettings()
        // themselves failed - a real backend problem, not "first run".
        // Showing it (with a retry) instead of silently routing to
        // onboarding avoids masking it behind a confusing later
        // "account already exists" error.
        if (!canceled) setScreen({ name: "error", message: errorMessage(unwrapError(thrown)) });
      });
    return () => {
      canceled = true;
    };
  }

  useEffect(() => {
    // Gated on ready so a Status()/GetSettings() failure is reported
    // through the already-loaded locale catalog (see errorMessage below);
    // running this before I18nProvider's own catalog fetch has resolved
    // would fall back to the raw, English-only backend error text.
    if (!ready) return;
    return load();
  }, [ready]);

  if (!ready || screen.name === "loading") {
    return (
      <main className="application-shell">
        <p>{t("common.loading")}</p>
      </main>
    );
  }

  switch (screen.name) {
    case "onboarding":
      return (
        <Onboarding
          onAccountReady={(account) => setScreen({ name: "shell", account })}
          onSwitchToUnlock={(databasePath) => setScreen({ name: "unlock", databasePath })}
        />
      );
    case "unlock":
      return (
        <Unlock
          databasePath={screen.databasePath}
          onAccountReady={(account) => setScreen({ name: "shell", account })}
          onSwitchToOnboarding={() => setScreen({ name: "onboarding" })}
        />
      );
    case "shell":
      return (
        <Suspense
          fallback={
            <main className="application-shell">
              <p>{t("common.loading")}</p>
            </main>
          }
        >
          <Shell account={screen.account} onLocked={load} />
        </Suspense>
      );
    case "error":
      return (
        <main className="application-shell">
          <p className="error" role="alert">
            {screen.message}
          </p>
          <button type="button" onClick={load}>
            {t("common.retry")}
          </button>
        </main>
      );
  }
}

/**
 * resolveInitialScreen decides where the app lands on startup, and again
 * after a lock: the currently unlocked account if one is already open
 * (for example, after Shell's own Lock button re-resolves), otherwise the
 * returning-user unlock form if a previous local account is on record, or
 * onboarding for a first run.
 */
async function resolveInitialScreen(): Promise<Screen> {
  const status = await accountStatus();
  if (status.unlocked && status.account) {
    return { name: "shell", account: status.account };
  }
  const settings = await getSettings();
  if (settings.last_database_path) {
    return { name: "unlock", databasePath: settings.last_database_path };
  }
  return { name: "onboarding" };
}

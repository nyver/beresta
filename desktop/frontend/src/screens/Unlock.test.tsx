import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { fakeAccountInfo, mockLocaleCatalog, mockSettings } from "../testUtils";
import { Unlock } from "./Unlock";

function renderUnlock(databasePath = "C:\\Users\\test\\Beresta\\beresta.db") {
  mockLocaleCatalog();
  mockSettings({ last_database_path: databasePath });

  const onAccountReady = vi.fn();
  const onSwitchToOnboarding = vi.fn();
  render(
    <I18nProvider>
      <Unlock
        databasePath={databasePath}
        onAccountReady={onAccountReady}
        onSwitchToOnboarding={onSwitchToOnboarding}
      />
    </I18nProvider>,
  );
  return { onAccountReady, onSwitchToOnboarding };
}

describe("Unlock", () => {
  it("unlocks and reports the account ready on success", async () => {
    const account = fakeAccountInfo();
    appMock.UnlockAccount.mockResolvedValue(account);
    const { onAccountReady } = renderUnlock();
    const user = userEvent.setup();

    await user.type(
      await screen.findByLabelText("onboarding.passphrase_label"),
      "correct horse battery",
    );
    await user.click(screen.getByRole("button", { name: "unlock.button" }));

    await waitFor(() => expect(onAccountReady).toHaveBeenCalledWith(account));
    expect(appMock.UnlockAccount).toHaveBeenCalledWith({
      database_path: "C:\\Users\\test\\Beresta\\beresta.db",
      passphrase: "correct horse battery",
    });
  });

  it("shows the uniform unlock-failed message on a wrong passphrase", async () => {
    appMock.UnlockAccount.mockRejectedValue(
      new Error(JSON.stringify({ code: "unlock_failed", message: "wrong" })),
    );
    renderUnlock();
    const user = userEvent.setup();

    await user.type(
      await screen.findByLabelText("onboarding.passphrase_label"),
      "wrong passphrase",
    );
    await user.click(screen.getByRole("button", { name: "unlock.button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.unlock_failed");
  });

  it("lets the user switch back to onboarding", async () => {
    const { onSwitchToOnboarding } = renderUnlock();
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "unlock.switch_to_onboarding" }),
    );
    expect(onSwitchToOnboarding).toHaveBeenCalled();
  });
});

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { fakeAccountInfo, mockLocaleCatalog, mockSettings } from "../testUtils";
import { Unlock } from "./Unlock";
import { main } from "../../wailsjs/go/models";

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

  it("unlocks when Enter is pressed in the passphrase field", async () => {
    const account = fakeAccountInfo();
    appMock.UnlockAccount.mockResolvedValue(account);
    const { onAccountReady } = renderUnlock();
    const user = userEvent.setup();

    await user.type(
      await screen.findByLabelText("onboarding.passphrase_label"),
      "correct horse battery{Enter}",
    );

    await waitFor(() => expect(onAccountReady).toHaveBeenCalledWith(account));
    expect(appMock.UnlockAccount).toHaveBeenCalledTimes(1);
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

  it("keeps the wipe action disabled until the confirmation phrase is typed exactly", async () => {
    renderUnlock();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "unlock.wipe_start_link" }));
    const confirmButton = screen.getByRole("button", { name: "unlock.wipe_confirm_button" });
    expect(confirmButton).toBeDisabled();

    const confirmInput = screen.getByLabelText("unlock.wipe_confirm_instructions");
    await user.type(confirmInput, "delete");
    expect(confirmButton).toBeDisabled();

    await user.clear(confirmInput);
    await user.type(confirmInput, "erase");
    expect(confirmButton).toBeEnabled();

    expect(appMock.WipeLocalAccount).not.toHaveBeenCalled();
  });

  it("wipes the local account and returns to onboarding once confirmed", async () => {
    appMock.WipeLocalAccount.mockResolvedValue(undefined);
    const databasePath = "C:\\Users\\test\\Beresta\\beresta.db";
    const { onSwitchToOnboarding } = renderUnlock(databasePath);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "unlock.wipe_start_link" }));
    await user.type(screen.getByLabelText("unlock.wipe_confirm_instructions"), "ERASE");
    await user.click(screen.getByRole("button", { name: "unlock.wipe_confirm_button" }));

    await waitFor(() => expect(appMock.WipeLocalAccount).toHaveBeenCalledWith(databasePath));
    await waitFor(() => expect(onSwitchToOnboarding).toHaveBeenCalled());
  });

  it("shows a localized error and stays on the unlock screen when the wipe fails", async () => {
    appMock.WipeLocalAccount.mockRejectedValue(
      new Error(JSON.stringify({ code: "internal", message: "disk error" })),
    );
    const { onSwitchToOnboarding } = renderUnlock();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "unlock.wipe_start_link" }));
    await user.type(screen.getByLabelText("unlock.wipe_confirm_instructions"), "ERASE");
    await user.click(screen.getByRole("button", { name: "unlock.wipe_confirm_button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
    expect(onSwitchToOnboarding).not.toHaveBeenCalled();
  });

  it("cancels the wipe confirmation without calling WipeLocalAccount", async () => {
    renderUnlock();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "unlock.wipe_start_link" }));
    await user.type(screen.getByLabelText("unlock.wipe_confirm_instructions"), "ERASE");
    await user.click(screen.getByRole("button", { name: "common.cancel" }));

    expect(screen.queryByText("unlock.wipe_warning")).not.toBeInTheDocument();
    expect(appMock.WipeLocalAccount).not.toHaveBeenCalled();
  });

  it("disables the wipe action while an unlock attempt is in flight, so the two cannot race", async () => {
    let resolveUnlock: (account: main.AccountInfo) => void = () => {};
    appMock.UnlockAccount.mockReturnValue(new Promise((resolve) => (resolveUnlock = resolve)));
    renderUnlock();
    const user = userEvent.setup();

    await user.type(await screen.findByLabelText("onboarding.passphrase_label"), "some passphrase");
    await user.click(screen.getByRole("button", { name: "unlock.button" }));

    // UnlockAccount is still pending: the wipe entry point must be inert
    // while it might be about to open a connection to the very database
    // WipeLocalAccount would delete.
    expect(screen.getByRole("button", { name: "unlock.wipe_start_link" })).toBeDisabled();

    resolveUnlock(fakeAccountInfo());
    await waitFor(() => expect(screen.getByRole("button", { name: "unlock.wipe_start_link" })).toBeEnabled());
  });

  it("disables the unlock submit while a wipe confirmation is in flight, so the two cannot race", async () => {
    let resolveWipe: () => void = () => {};
    appMock.WipeLocalAccount.mockReturnValue(new Promise<void>((resolve) => (resolveWipe = resolve)));
    renderUnlock();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "unlock.wipe_start_link" }));
    await user.type(screen.getByLabelText("unlock.wipe_confirm_instructions"), "ERASE");
    await user.click(screen.getByRole("button", { name: "unlock.wipe_confirm_button" }));

    expect(screen.getByRole("button", { name: "unlock.button" })).toBeDisabled();

    resolveWipe();
    await waitFor(() => expect(appMock.WipeLocalAccount).toHaveBeenCalled());
  });
});

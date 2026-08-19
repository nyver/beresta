import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { I18nProvider } from "../i18n";
import { appMock } from "../setupTests";
import { fakeAccountInfo, mockLocaleCatalog, mockSettings } from "../testUtils";
import { Onboarding } from "./Onboarding";

function renderOnboarding() {
  mockLocaleCatalog();
  mockSettings();
  appMock.DefaultDatabasePath.mockResolvedValue("C:\\Users\\test\\Beresta\\beresta.db");

  const onAccountReady = vi.fn();
  const onSwitchToUnlock = vi.fn();
  render(
    <I18nProvider>
      <Onboarding onAccountReady={onAccountReady} onSwitchToUnlock={onSwitchToUnlock} />
    </I18nProvider>,
  );
  return { onAccountReady, onSwitchToUnlock };
}

describe("Onboarding", () => {
  it("selects the local-only mode by default and shows the create form", async () => {
    renderOnboarding();

    const localCard = await screen.findByRole("radio", { name: /mode_local_title/ });
    expect(localCard).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("button", { name: "onboarding.create_button" })).toBeInTheDocument();
  });

  it("does not block local account creation when the server card is chosen", async () => {
    renderOnboarding();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("radio", { name: /mode_server_title/ }));
    expect(screen.queryByRole("button", { name: "onboarding.create_button" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "onboarding.mode_local_title" }));
    expect(screen.getByRole("button", { name: "onboarding.create_button" })).toBeInTheDocument();
  });

  it("rejects a passphrase that is too short without calling CreateAccount", async () => {
    renderOnboarding();
    const user = userEvent.setup();

    await user.type(await screen.findByLabelText("onboarding.passphrase_label"), "short");
    await user.type(screen.getByLabelText("onboarding.passphrase_confirm_label"), "short");
    await user.click(screen.getByRole("button", { name: "onboarding.create_button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("onboarding.passphrase_too_short");
    expect(appMock.CreateAccount).not.toHaveBeenCalled();
  });

  it("rejects mismatched passphrases without calling CreateAccount", async () => {
    renderOnboarding();
    const user = userEvent.setup();

    await user.type(await screen.findByLabelText("onboarding.passphrase_label"), "correct horse battery");
    await user.type(screen.getByLabelText("onboarding.passphrase_confirm_label"), "different horse battery");
    await user.click(screen.getByRole("button", { name: "onboarding.create_button" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("onboarding.passphrase_mismatch");
    expect(appMock.CreateAccount).not.toHaveBeenCalled();
  });

  it("creates the account and reports it ready on success", async () => {
    const account = fakeAccountInfo();
    appMock.CreateAccount.mockResolvedValue(account);
    const { onAccountReady } = renderOnboarding();
    const user = userEvent.setup();

    await waitFor(() =>
      expect(screen.getByDisplayValue("C:\\Users\\test\\Beresta\\beresta.db")).toBeInTheDocument(),
    );
    await user.type(await screen.findByLabelText("onboarding.passphrase_label"), "correct horse battery");
    await user.type(screen.getByLabelText("onboarding.passphrase_confirm_label"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "onboarding.create_button" }));

    await waitFor(() => expect(onAccountReady).toHaveBeenCalledWith(account));
    expect(appMock.CreateAccount).toHaveBeenCalledWith({
      database_path: "C:\\Users\\test\\Beresta\\beresta.db",
      passphrase: "correct horse battery",
    });
  });

  it("persists a language change through UpdateSettings", async () => {
    renderOnboarding();
    const user = userEvent.setup();

    await user.selectOptions(await screen.findByLabelText("onboarding.language_label"), "ru");

    await waitFor(() =>
      expect(appMock.UpdateSettings).toHaveBeenCalledWith(
        expect.objectContaining({ language: "ru" }),
      ),
    );
  });

  it("switches to the unlock screen when an account already exists at the chosen path", async () => {
    appMock.CreateAccount.mockRejectedValue(
      new Error(JSON.stringify({ code: "account_exists", message: "exists" })),
    );
    const { onSwitchToUnlock } = renderOnboarding();
    const user = userEvent.setup();

    await waitFor(() =>
      expect(screen.getByDisplayValue("C:\\Users\\test\\Beresta\\beresta.db")).toBeInTheDocument(),
    );
    await user.type(await screen.findByLabelText("onboarding.passphrase_label"), "correct horse battery");
    await user.type(screen.getByLabelText("onboarding.passphrase_confirm_label"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "onboarding.create_button" }));

    await waitFor(() =>
      expect(onSwitchToUnlock).toHaveBeenCalledWith("C:\\Users\\test\\Beresta\\beresta.db"),
    );
  });
});

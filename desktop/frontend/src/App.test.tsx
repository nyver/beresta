import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { App } from "./App";
import { appMock } from "./setupTests";
import { fakeAccountInfo, mockLocaleCatalog, mockLockedStatus, mockSettings, mockUnlockedStatus } from "./testUtils";

describe("App", () => {
  it("shows onboarding on a first run with no prior local account", async () => {
    mockLocaleCatalog();
    mockLockedStatus();
    mockSettings({ last_database_path: "" });

    render(<App />);

    expect(await screen.findByRole("radio", { name: /mode_local_title/ })).toBeInTheDocument();
  });

  it("shows the unlock screen when a prior local account is on record", async () => {
    mockLocaleCatalog();
    mockLockedStatus();
    mockSettings({ last_database_path: "C:\\Users\\test\\Beresta\\beresta.db" });

    render(<App />);

    expect(await screen.findByRole("button", { name: "unlock.button" })).toBeInTheDocument();
  });

  it("shows the shell directly when an account is already unlocked", async () => {
    mockLocaleCatalog();
    const account = fakeAccountInfo();
    mockUnlockedStatus(account);
    mockSettings();

    render(<App />);

    expect(await screen.findByRole("button", { name: "shell.lock_button" })).toBeInTheDocument();
  });

  it("shows a retryable error instead of silently starting onboarding when Status() fails", async () => {
    mockLocaleCatalog();
    mockSettings();
    appMock.Status.mockRejectedValueOnce(
      new Error(JSON.stringify({ code: "internal", message: "database is locked" })),
    );

    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent("errors.internal");
    expect(screen.queryByRole("radio", { name: /mode_local_title/ })).not.toBeInTheDocument();

    mockLockedStatus();
    await userEvent.setup().click(screen.getByRole("button", { name: "common.retry" }));

    expect(await screen.findByRole("radio", { name: /mode_local_title/ })).toBeInTheDocument();
  });
});

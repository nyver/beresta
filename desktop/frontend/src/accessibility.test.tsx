import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { App } from "./App";
import { appMock } from "./setupTests";
import {
  fakeAccountInfo,
  fakeNote,
  fakeTag,
  mockLocaleCatalog,
  mockLockedStatus,
  mockSavedSearches,
  mockSettings,
  mockSyncStatus,
  mockUnlockedStatus,
} from "./testUtils";

function expectNamedInteractiveControls(container: HTMLElement) {
  const controls = container.querySelectorAll<HTMLElement>(
    'button, input, select, textarea, a[href], [role="button"], [role="radio"]',
  );
  expect(controls.length).toBeGreaterThan(0);
  for (const control of controls) {
    expect(control).toHaveAccessibleName();
  }

  for (const image of container.querySelectorAll("img")) {
    expect(image).toHaveAttribute("alt");
  }
}

describe("desktop accessibility acceptance", () => {
  it("gives every onboarding control an accessible name", async () => {
    mockLocaleCatalog();
    mockLockedStatus();
    mockSettings({ last_database_path: "" });

    const { container } = render(<App />);

    await screen.findByRole("radio", { name: /mode_local_title/ });
    expectNamedInteractiveControls(container);
  });

  it("gives every unlock control an accessible name", async () => {
    mockLocaleCatalog();
    mockLockedStatus();
    mockSettings({ last_database_path: "C:\\data\\beresta.db" });

    const { container } = render(<App />);

    await screen.findByRole("button", { name: "unlock.button" });
    expectNamedInteractiveControls(container);
  });

  it("exposes named navigation, list, editor, and command controls in the shell", async () => {
    mockLocaleCatalog();
    mockUnlockedStatus(fakeAccountInfo());
    mockSettings();
    mockSavedSearches();
    mockSyncStatus();
    appMock.AutostartStatus.mockResolvedValue({ enabled: false, conflict_path: "" });
    appMock.ListNotebooks.mockResolvedValue([]);
    appMock.ListTags.mockResolvedValue([fakeTag({ name: "Important" })]);
    appMock.ListNotes.mockResolvedValue([fakeNote({ title: "Accessible note" })]);

    const { container } = render(<App />);

    await screen.findByRole("option", { name: /Accessible note/ }, { timeout: 5_000 });
    expect(screen.getByRole("main")).toHaveAccessibleName();
    expect(screen.getByRole("navigation", { name: "shell.notebooks_section" })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "shell.tags_section" })).toBeInTheDocument();
    expect(screen.getByRole("listbox", { name: "shell.title" })).toBeInTheDocument();
    expectNamedInteractiveControls(container);
  });
});

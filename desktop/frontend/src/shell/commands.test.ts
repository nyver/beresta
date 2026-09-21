import { describe, expect, it } from "vitest";

import { matchesShortcut, SHELL_COMMANDS } from "./commands";

function keydown(init: Partial<KeyboardEventInit> & { key: string }): KeyboardEvent {
  return new KeyboardEvent("keydown", init);
}

describe("matchesShortcut", () => {
  it("matches Ctrl plus the accelerator key", () => {
    expect(matchesShortcut(keydown({ key: "n", ctrlKey: true }), SHELL_COMMANDS.newNote.shortcut)).toBe(true);
  });

  it("is case-insensitive on the key (Shift can flip letter case without changing the shortcut)", () => {
    expect(matchesShortcut(keydown({ key: "N", ctrlKey: true }), SHELL_COMMANDS.newNote.shortcut)).toBe(true);
  });

  it("accepts Cmd (metaKey) as well as Ctrl, for non-Windows Wails builds", () => {
    expect(matchesShortcut(keydown({ key: "n", metaKey: true }), SHELL_COMMANDS.newNote.shortcut)).toBe(true);
  });

  it("does not match without a Ctrl/Cmd modifier", () => {
    expect(matchesShortcut(keydown({ key: "n" }), SHELL_COMMANDS.newNote.shortcut)).toBe(false);
  });

  it("does not match a different key", () => {
    expect(matchesShortcut(keydown({ key: "b", ctrlKey: true }), SHELL_COMMANDS.newNote.shortcut)).toBe(false);
  });

  it("requires Shift when the shortcut declares it", () => {
    const shortcut = SHELL_COMMANDS.toggleNavigation.shortcut;
    expect(matchesShortcut(keydown({ key: "s", ctrlKey: true }), shortcut)).toBe(false);
    expect(matchesShortcut(keydown({ key: "s", ctrlKey: true, shiftKey: true }), shortcut)).toBe(true);
  });

  it("rejects Shift when the shortcut does not declare it", () => {
    expect(matchesShortcut(keydown({ key: "n", ctrlKey: true, shiftKey: true }), SHELL_COMMANDS.newNote.shortcut)).toBe(
      false,
    );
  });
});

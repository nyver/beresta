/**
 * The desktop shell's command registry (task 8.3, design.md decision 7's
 * "A command registry supplies shortcuts, accessible names, menu labels,
 * and availability so keyboard and pointer actions invoke the same
 * command."): one canonical list of the shell-level actions that must stay
 * reachable by more than one input method. Each entry names the i18n key
 * already used by that command's pointer control (button or menu item), so
 * a future menu/tooltip surface (task 8.4) can render "label (shortcut)"
 * from a single source instead of duplicating either string.
 *
 * Not every command below carries a `shortcut` here:
 * - "quickNote" is triggered by the user's own configurable global hotkey
 *   (ShellIntegrationPanel's `quick_note_hotkey` setting, registered with
 *   Windows through desktop/platform/traymenu) rather than a fixed in-app
 *   accelerator, so binding a second, different key here would just add a
 *   confusing alternate trigger.
 * - "linkFormat"/"boldFormat"/"italicFormat" are handled entirely by
 *   Quill's own default keyboard bindings (see NoteEditor.tsx's toolbar
 *   config, which includes "link" - Quill's snow theme registers Ctrl+K
 *   for it automatically once a link toolbar button exists; bold/italic's
 *   Ctrl+B/Ctrl+I come from quill/modules/keyboard.js's own DEFAULTS).
 *   They are listed here only so this registry stays the complete map of
 *   "editor formatting/link" commands the spec requires, for a future
 *   tooltip/menu surface to read.
 * - "dismiss" (Escape) is handled locally by whichever transient surface
 *   is open (Modal's focus trap, SearchBar's clear-then-blur convention),
 *   since each owns the context needed to decide what "dismiss" means at
 *   that moment; a single global handler here could not do that safely.
 */
export interface ShellCommandShortcut {
  /** Human-readable accelerator text for tooltips/menus, e.g. "Ctrl+N".
   * Key combinations are not translated - they are the same physical keys
   * regardless of UI language. */
  display: string;
  key: string;
  shiftKey?: boolean;
}

export interface ShellCommand {
  id: string;
  /** i18n key for this command's accessible name, matching the label its
   * pointer control (button/menu item) already uses. Omitted for a command
   * whose only pointer control is Quill's own editor toolbar, which
   * supplies its own icon/label rather than reading this catalog (task
   * 8.4 covers making those toolbar buttons themselves accessible). */
  labelKey?: string;
  shortcut?: ShellCommandShortcut;
}

export const SHELL_COMMANDS = {
  newNote: { id: "new-note", labelKey: "shell.new_note_button", shortcut: { display: "Ctrl+N", key: "n" } },
  quickNote: { id: "quick-note", labelKey: "shell.quick_note_button" },
  search: { id: "search", labelKey: "search.placeholder", shortcut: { display: "Ctrl+F", key: "f" } },
  settings: { id: "settings", labelKey: "settings.title", shortcut: { display: "Ctrl+,", key: "," } },
  toggleNavigation: {
    id: "toggle-navigation",
    labelKey: "shell.collapse_sidebar",
    shortcut: { display: "Ctrl+Shift+S", key: "s", shiftKey: true },
  },
  dismiss: { id: "dismiss", labelKey: "common.close", shortcut: { display: "Esc", key: "escape" } },
  boldFormat: { id: "bold-format", shortcut: { display: "Ctrl+B", key: "b" } },
  italicFormat: { id: "italic-format", shortcut: { display: "Ctrl+I", key: "i" } },
  linkFormat: { id: "link-format", shortcut: { display: "Ctrl+K", key: "k" } },
  lock: { id: "lock", labelKey: "shell.lock_button", shortcut: { display: "Ctrl+L", key: "l" } },
} as const satisfies Record<string, ShellCommand>;

/** Whether a keydown event's accelerator (Ctrl or Cmd, plus Shift when the
 * shortcut requires it) matches shortcut. Windows only ever sends ctrlKey,
 * but metaKey is accepted too so this stays correct if this ever runs
 * under Wails' Linux/macOS builds. */
export function matchesShortcut(event: KeyboardEvent, shortcut: ShellCommandShortcut): boolean {
  return (
    (event.ctrlKey || event.metaKey) &&
    event.key.toLowerCase() === shortcut.key.toLowerCase() &&
    Boolean(event.shiftKey) === Boolean(shortcut.shiftKey)
  );
}

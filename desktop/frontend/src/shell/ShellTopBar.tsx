import { useI18n } from "../i18n";
import type { SyncState } from "../api";

export interface ShellTopBarProps {
  titleId: string;
  sidebarCollapsed: boolean;
  onToggleSidebar: () => void;
  focusMode: boolean;
  onToggleFocusMode: () => void;
  /** account.key_protection, e.g. "windows-hello" | "dpapi" | "". */
  keyProtection: string;
  syncStatus: SyncState | null;
  forcingSync: boolean;
  onSyncNow: () => void;
  onOpenSync: () => void;
  onOpenSettings: () => void;
  onOpenQuickNote: () => void;
  onLock: () => void;
  locking: boolean;
}

/**
 * ShellTopBar is the desktop shell's stable global control bar (task 8.1's
 * "stable top bar" region, specs/windows-desktop-client's "Stable
 * three-pane desktop workspace" requirement): sidebar/focus toggles, the
 * workspace title, key-protection hint, synchronization status/controls,
 * settings entry, and lock. Data, device, and diagnostic actions live
 * behind the settings button's grouped panel (task 7.9), not as their own
 * permanent controls here.
 */
export function ShellTopBar({
  titleId,
  sidebarCollapsed,
  onToggleSidebar,
  focusMode,
  onToggleFocusMode,
  keyProtection,
  syncStatus,
  forcingSync,
  onSyncNow,
  onOpenSync,
  onOpenSettings,
  onOpenQuickNote,
  onLock,
  locking,
}: ShellTopBarProps) {
  const { t } = useI18n();

  return (
    <header className="shell-topbar">
      <div className="shell-topbar-lead">
        <button
          type="button"
          className="icon-button"
          aria-label={sidebarCollapsed ? t("shell.expand_sidebar") : t("shell.collapse_sidebar")}
          title={sidebarCollapsed ? t("shell.expand_sidebar") : t("shell.collapse_sidebar")}
          // Focus mode already hides the sidebar regardless of
          // sidebarCollapsed's own value (see Shell.tsx's shell-body class
          // list); disabling this button while it's active avoids a click
          // here silently changing state with no visible effect.
          disabled={focusMode}
          onClick={onToggleSidebar}
        >
          ☰
        </button>
        <button
          type="button"
          className={`icon-button${focusMode ? " active" : ""}`}
          aria-label={focusMode ? t("shell.exit_focus_mode") : t("shell.enter_focus_mode")}
          title={focusMode ? t("shell.exit_focus_mode") : t("shell.enter_focus_mode")}
          aria-pressed={focusMode}
          onClick={onToggleFocusMode}
        >
          ⛶
        </button>
        <h1 id={titleId}>{t("shell.title")}</h1>
      </div>
      <div className="shell-topbar-actions">
        {keyProtection ? (
          <span className="key-protection-hint">
            <span aria-hidden="true">🔒</span>{" "}
            <span>
              {keyProtection === "windows-hello" ? t("shell.key_protection_hello") : t("shell.key_protection_dpapi")}
            </span>
          </span>
        ) : null}
        <button
          type="button"
          className="icon-button quick-note-button"
          aria-label={t("shell.quick_note_button")}
          title={t("shell.quick_note_button")}
          onClick={onOpenQuickNote}
        >
          <span aria-hidden="true">📝</span>
        </button>
        <button
          type="button"
          className={`sync-status-pill sync-status-${syncStatus ?? "local_only"}`}
          aria-label={t("sync.open_button")}
          title={t("sync.open_button")}
          onClick={onOpenSync}
        >
          <span className="sync-status-dot" aria-hidden="true" />
          {syncStatus ? t(`sync.status_${syncStatus}`) : t("sync.open_button")}
        </button>
        <button
          type="button"
          className="icon-button sync-now-button"
          aria-label={t("sync.force_button")}
          title={t("sync.force_button")}
          aria-busy={forcingSync}
          disabled={forcingSync || syncStatus === null || syncStatus === "local_only"}
          onClick={onSyncNow}
        >
          <span aria-hidden="true">↻</span>
        </button>
        <button
          type="button"
          className="icon-button"
          aria-label={t("settings.title")}
          title={t("settings.title")}
          onClick={onOpenSettings}
        >
          ⚙
        </button>
        <button
          type="button"
          className="icon-button"
          aria-label={t("shell.lock_button")}
          title={t("shell.lock_button")}
          onClick={onLock}
          disabled={locking}
        >
          <span aria-hidden="true">🔒</span>
        </button>
      </div>
    </header>
  );
}

import { useI18n } from "../i18n";

export interface UndoSnackbarProps {
  /** Localized message describing what just happened (for example, "Note
   * deleted"). */
  message: string;
  /** Reverses the action. The caller is responsible for dismissing the
   * snackbar itself once undo starts. */
  onUndo: () => void;
  /** Dismisses the snackbar without undoing anything - both an explicit
   * close and the caller's own auto-dismiss timer route through this. */
  onDismiss: () => void;
}

/**
 * UndoSnackbar is a transient, non-blocking bottom-of-screen notification
 * offering a single reversal action, backing the "Recoverable ordinary
 * note deletion" requirement in specs/notes-management: deleting a note
 * removes it from the list immediately, with undo as the safety net
 * instead of a confirmation prompt. Unlike Modal, it never traps focus or
 * blocks interaction with the rest of the shell.
 */
export function UndoSnackbar({ message, onUndo, onDismiss }: UndoSnackbarProps) {
  const { t } = useI18n();
  return (
    <div className="undo-snackbar" role="status" aria-live="polite">
      <span className="undo-snackbar-message">{message}</span>
      <button type="button" className="link-button undo-snackbar-undo" onClick={onUndo}>
        {t("common.undo")}
      </button>
      <button
        type="button"
        className="undo-snackbar-dismiss"
        aria-label={t("common.close")}
        onClick={onDismiss}
      >
        ×
      </button>
    </div>
  );
}

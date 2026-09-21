import { useI18n } from "../i18n";

export interface LoadingStateProps {
  /** Overrides the default "common.loading" text - use for a
   * section-specific loading message (for example, "Verifying backup..."). */
  label?: string;
}

/**
 * Shared loading indicator (task 10.5's "reusable accessible... loading...
 * state"). `role="status"` lets assistive technology announce the loading
 * text without the interruption `role="alert"` (ErrorState below) would
 * cause, matching the "polite" convention `UndoSnackbar` already uses for
 * its own non-error status.
 */
export function LoadingState({ label }: LoadingStateProps) {
  const { t } = useI18n();
  return (
    <p className="hint" role="status">
      {label ?? t("common.loading")}
    </p>
  );
}

export interface ErrorStateProps {
  /** Already-localized error message. */
  message: string;
  /** Omit when the error has no retry action (for example, a form-field
   * validation error the user resolves by re-submitting). */
  onRetry?: () => void;
  /** Overrides the default "common.retry" label. */
  retryLabel?: string;
}

/**
 * Shared error (+ optional retry) presentation (task 10.5's "reusable
 * accessible... error, and retry" states), covering both the bare
 * validation-message case and the "something failed, here's how to try
 * again" case with one component instead of every screen re-deriving the
 * same `<p className="error" role="alert">` markup independently.
 */
export function ErrorState({ message, onRetry, retryLabel }: ErrorStateProps) {
  const { t } = useI18n();
  return (
    <div className="error-state">
      <p className="error" role="alert">
        {message}
      </p>
      {onRetry ? (
        <button type="button" onClick={onRetry}>
          {retryLabel ?? t("common.retry")}
        </button>
      ) : null}
    </div>
  );
}

import { formatClockTime } from "../format";
import { useI18n } from "../i18n";
import type { SyncStatusValue } from "../api";
import type { NoteSaveState } from "../editor/NoteEditor";

export interface SaveStatusLineProps {
  saveState: NoteSaveState;
  syncStatus: SyncStatusValue | null;
  syncedAt: number | null;
  onOpenSync: () => void;
}

/**
 * SaveStatusLine is the open note's footer status text (task: replace the
 * topbar's big Sync button with passive state - "the user cares more about
 * what happened than a button to press"). It combines this note's own local
 * save state (useNoteDocument, via NoteEditor's onSaveStateChange) with the
 * workspace-wide synchronization status Shell already tracks for the
 * topbar's compact sync pill, so the two surfaces never disagree.
 */
export function SaveStatusLine({ saveState, syncStatus, syncedAt, onOpenSync }: SaveStatusLineProps) {
  const { t } = useI18n();

  // "Not saved" is reserved for a genuine failed-commit retry loop
  // (hasError) - a freshly created, never-yet-edited note has savedAt still
  // null too, but there is nothing pending or at risk in that state, so it
  // reads as "Saved locally" like any other clean state.
  const localLabel = saveState.saving
    ? t("status.saving")
    : saveState.hasError
      ? t("status.not_saved")
      : t("status.saved_locally");

  // "disabled" (local-only account) and null (not loaded yet) render no
  // sync fragment at all: there is nothing synchronization-related to
  // report yet, and the local-save half above already covers what an
  // offline-first user needs to know in that case.
  let syncFragment: string | null = null;
  if (syncStatus === "offline") syncFragment = t("sync.status_offline");
  else if (syncStatus === "active") syncFragment = t("sync.status_active");
  else if (syncStatus === "current") syncFragment = t("sync.status_current");
  else if (syncStatus === "failed") syncFragment = t("sync.status_failed");

  const clickable = syncStatus === "offline" || syncStatus === "failed";

  return (
    <p className="save-status-line">
      <span>{localLabel}</span>
      {syncFragment ? (
        <>
          <span aria-hidden="true"> · </span>
          {clickable ? (
            <button type="button" className="link-button save-status-sync-link" onClick={onOpenSync}>
              {syncFragment}
            </button>
          ) : (
            <span>
              {syncFragment}
              {syncStatus === "current" && syncedAt !== null ? ` ${formatClockTime(syncedAt)}` : ""}
            </span>
          )}
        </>
      ) : null}
    </p>
  );
}

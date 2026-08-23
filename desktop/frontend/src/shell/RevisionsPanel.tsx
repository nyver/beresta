import { useCallback, useEffect, useState } from "react";

import { diffRevisions, listRevisions, restoreRevision, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface RevisionsPanelProps {
  noteId: string;
  /** Awaited immediately before RestoreRevision is called, so the caller
   * can flush the live editor's own not-yet-debounced pending edit into a
   * revision of its own first. Without this, restoring while an edit is
   * still queued would let that stale edit get flushed (by the editor's
   * own unmount-time cleanup, triggered by onRestored below) on top of
   * the just-restored content, silently reintroducing what the user
   * asked to discard. */
  onBeforeRestore: () => Promise<void>;
  /** Called after a revision has been durably restored, so the caller can
   * reload the live editor content (RestoreRevision commits a new current
   * revision through the normal note-body path, which the already-open
   * Yjs document does not otherwise know to refetch). */
  onRestored: () => void;
}

/**
 * RevisionsPanel lists a note's retained revision history (newest first),
 * shows a line-based diff of the selected revision against the one
 * immediately before it, and offers rollback: RestoreRevision creates a
 * new current revision matching the selected one's plain-text content
 * without erasing any intervening history (task 5.7).
 */
export function RevisionsPanel({ noteId, onBeforeRestore, onRestored }: RevisionsPanelProps) {
  const { t, errorMessage, ready } = useI18n();
  const [revisions, setRevisions] = useState<main.RevisionDTO[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState("");
  const [diffLines, setDiffLines] = useState<main.DiffLineDTO[] | null>(null);
  const [diffError, setDiffError] = useState<string | null>(null);
  const [confirmingRestore, setConfirmingRestore] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [restoreError, setRestoreError] = useState<string | null>(null);

  const refresh = useCallback(() => {
    listRevisions(noteId)
      .then(setRevisions)
      .catch((thrown: unknown) => setLoadError(errorMessage(unwrapError(thrown))));
  }, [noteId, errorMessage]);

  useEffect(() => {
    // Gated on ready, same as AttachmentPanel's identical reset effect: a
    // failed fetch needs the locale catalog already loaded so errorMessage
    // can localize it instead of falling back to raw backend error text.
    if (!ready) return;
    setLoadError(null);
    setSelectedId("");
    setDiffLines(null);
    setDiffError(null);
    setConfirmingRestore(false);
    setRestoreError(null);
    refresh();
    // Deliberately keyed on noteId/ready, not on refresh: see
    // AttachmentPanel's identical reasoning (refresh's identity also
    // shifts whenever errorMessage does, which would otherwise re-run
    // this reset-and-refetch a second time right after the locale catalog
    // finishes loading).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [noteId, ready]);

  function selectRevision(revisionId: string) {
    setSelectedId(revisionId);
    setConfirmingRestore(false);
    setRestoreError(null);
    setDiffError(null);
    const index = revisions.findIndex((r) => r.id === revisionId);
    if (index === -1) return;
    const fromId = index > 0 ? revisions[index - 1].id : "";
    diffRevisions(noteId, fromId, revisionId)
      .then(setDiffLines)
      .catch((thrown: unknown) => setDiffError(errorMessage(unwrapError(thrown))));
  }

  async function handleRestore() {
    if (!selectedId) return;
    setRestoring(true);
    setRestoreError(null);
    try {
      await onBeforeRestore();
      await restoreRevision(noteId, selectedId);
      setConfirmingRestore(false);
      onRestored();
      refresh();
    } catch (thrown: unknown) {
      setRestoreError(errorMessage(unwrapError(thrown)));
    } finally {
      setRestoring(false);
    }
  }

  if (loadError) {
    return (
      <section className="revisions-panel" aria-label={t("revisions.section_title")}>
        <p className="error" role="alert">
          {loadError}
        </p>
      </section>
    );
  }

  // Newest first for display; ListRevisions itself returns oldest first
  // (the order selectRevision's from/to lookup relies on).
  const displayOrder = [...revisions].reverse();

  return (
    // No own heading here - it always renders inside NoteEditorPane's
    // History Modal now, whose title already says "History".
    <section className="revisions-panel" aria-label={t("revisions.section_title")}>
      {displayOrder.length === 0 ? (
        <p className="hint">{t("revisions.empty")}</p>
      ) : (
        <ul className="revision-list">
          {displayOrder.map((revision) => (
            <li key={revision.id}>
              <button
                type="button"
                className={`revision-row${revision.id === selectedId ? " selected" : ""}`}
                onClick={() => selectRevision(revision.id)}
              >
                <span>{new Date(revision.created_unix_ms).toLocaleString()}</span>
                {revision.checkpoint ? (
                  <span className="revision-checkpoint-badge">{t("revisions.checkpoint_badge")}</span>
                ) : null}
              </button>
            </li>
          ))}
        </ul>
      )}

      {selectedId ? (
        <div className="revision-detail">
          {diffError ? (
            <p className="error" role="alert">
              {diffError}
            </p>
          ) : diffLines === null ? (
            <p className="hint">{t("common.loading")}</p>
          ) : (
            <pre className="revision-diff">
              {diffLines.map((line, index) => (
                <div key={index} className={`revision-diff-line revision-diff-${line.op}`}>
                  <span className="revision-diff-marker" aria-hidden="true">
                    {line.op === "insert" ? "+" : line.op === "delete" ? "-" : " "}
                  </span>
                  {line.text}
                </div>
              ))}
            </pre>
          )}

          {restoreError ? (
            <p className="error" role="alert">
              {restoreError}
            </p>
          ) : null}
          {confirmingRestore ? (
            <div className="revision-restore-confirm">
              <span>{t("revisions.restore_confirm")}</span>
              <button type="button" disabled={restoring} onClick={() => void handleRestore()}>
                {restoring ? t("revisions.restoring") : t("revisions.restore_confirm_button")}
              </button>
              <button
                type="button"
                className="link-button"
                disabled={restoring}
                onClick={() => setConfirmingRestore(false)}
              >
                {t("common.cancel")}
              </button>
            </div>
          ) : (
            <button type="button" className="link-button" onClick={() => setConfirmingRestore(true)}>
              {t("revisions.restore_button")}
            </button>
          )}
        </div>
      ) : null}
    </section>
  );
}

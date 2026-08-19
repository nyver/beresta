import { useCallback, useEffect, useState } from "react";

import {
  createManualBackup,
  getSettings,
  listBackups,
  pickBackupDirectory,
  planRestore,
  previewBackup,
  restoreSelective,
  restoreWhole,
  unwrapError,
  updateSettings,
  verifyBackup,
} from "../api";
import { formatBytes } from "../format";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface BackupsPanelProps {
  /** Called after a successful restore, so the caller can reload its own
   * note/notebook/tag state - a restore mutates the live account data
   * this panel has no other way to tell the rest of the shell about. */
  onRestored: () => void;
}

const BACKUP_KINDS = ["daily", "manual", "pre_restore", "pre_migration"] as const;
type BackupKind = (typeof BACKUP_KINDS)[number];

interface DryRun {
  plan: main.RestorePlanDTO;
  selected: Set<string>;
}

/**
 * BackupsPanel covers task 5.7's backup surface: the external backup
 * directory setting, the catalog for each backup kind, a read-only
 * preview, a dry-run restore plan, and the two restore actions the
 * backend actually distinguishes - RestoreWhole (atomically replaces the
 * entire local database, always behind its own extra confirmation since
 * it is destructive) and RestoreSelective (imports chosen notes as new,
 * non-destructive local operations).
 */
export function BackupsPanel({ onRestored }: BackupsPanelProps) {
  const { t, errorMessage, ready } = useI18n();

  const [directory, setDirectory] = useState("");
  const [directoryError, setDirectoryError] = useState<string | null>(null);
  const [changingDirectory, setChangingDirectory] = useState(false);

  const [kind, setKind] = useState<BackupKind>("daily");
  const [backups, setBackups] = useState<main.BackupDTO[]>([]);
  const [listError, setListError] = useState<string | null>(null);

  const [creatingManual, setCreatingManual] = useState(false);
  const [manualError, setManualError] = useState<string | null>(null);

  const [selectedBackupId, setSelectedBackupId] = useState("");
  const [preview, setPreview] = useState<main.BackupPreviewDTO | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);

  const [dryRun, setDryRun] = useState<DryRun | null>(null);
  const [planning, setPlanning] = useState(false);
  const [planError, setPlanError] = useState<string | null>(null);

  const [confirmingWholeRestore, setConfirmingWholeRestore] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [restoreError, setRestoreError] = useState<string | null>(null);
  const [restoreSuccess, setRestoreSuccess] = useState(false);

  useEffect(() => {
    if (!ready) return;
    getSettings()
      .then((settings) => setDirectory(settings.backup_directory))
      .catch((thrown: unknown) => setDirectoryError(errorMessage(unwrapError(thrown))));
  }, [ready, errorMessage]);

  const refreshBackups = useCallback(() => {
    listBackups(kind)
      .then(setBackups)
      .catch((thrown: unknown) => setListError(errorMessage(unwrapError(thrown))));
  }, [kind, errorMessage]);

  useEffect(() => {
    if (!ready) return;
    setListError(null);
    refreshBackups();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, kind]);

  async function handleChangeDirectory() {
    setDirectoryError(null);
    let chosen: string;
    try {
      chosen = await pickBackupDirectory();
    } catch (thrown: unknown) {
      setDirectoryError(errorMessage(unwrapError(thrown)));
      return;
    }
    if (!chosen) return;
    setChangingDirectory(true);
    try {
      const current = await getSettings();
      const updated = await updateSettings({ ...current, backup_directory: chosen });
      setDirectory(updated.backup_directory);
    } catch (thrown: unknown) {
      setDirectoryError(errorMessage(unwrapError(thrown)));
    } finally {
      setChangingDirectory(false);
    }
  }

  async function handleCreateManualBackup() {
    if (!directory) return;
    setCreatingManual(true);
    setManualError(null);
    try {
      await createManualBackup(directory);
      if (kind === "manual") refreshBackups();
    } catch (thrown: unknown) {
      setManualError(errorMessage(unwrapError(thrown)));
    } finally {
      setCreatingManual(false);
    }
  }

  function handlePreview(backupId: string) {
    setSelectedBackupId(backupId);
    setPreview(null);
    setPreviewError(null);
    setDryRun(null);
    setPlanError(null);
    setConfirmingWholeRestore(false);
    setRestoreError(null);
    setRestoreSuccess(false);
    previewBackup(backupId)
      .then(setPreview)
      .catch((thrown: unknown) => setPreviewError(errorMessage(unwrapError(thrown))));
  }

  function closePreview() {
    setSelectedBackupId("");
    setPreview(null);
    setDryRun(null);
  }

  async function handleVerify(backupId: string) {
    try {
      await verifyBackup(backupId);
      refreshBackups();
    } catch (thrown: unknown) {
      setListError(errorMessage(unwrapError(thrown)));
    }
  }

  async function handleStartRestore() {
    if (!selectedBackupId) return;
    setPlanning(true);
    setPlanError(null);
    try {
      const plan = await planRestore(selectedBackupId, []);
      const selected = new Set(
        plan.entries.filter((entry) => entry.kind !== "unchanged").map((entry) => entry.note_id),
      );
      setDryRun({ plan, selected });
    } catch (thrown: unknown) {
      setPlanError(errorMessage(unwrapError(thrown)));
    } finally {
      setPlanning(false);
    }
  }

  function toggleEntrySelected(noteId: string) {
    setDryRun((current) => {
      if (!current) return current;
      const selected = new Set(current.selected);
      if (selected.has(noteId)) {
        selected.delete(noteId);
      } else {
        selected.add(noteId);
      }
      return { ...current, selected };
    });
  }

  async function handleRestoreSelected() {
    if (!dryRun || !selectedBackupId || !directory || dryRun.selected.size === 0) return;
    setRestoring(true);
    setRestoreError(null);
    try {
      await restoreSelective(selectedBackupId, [...dryRun.selected], directory);
      setRestoreSuccess(true);
      setDryRun(null);
      onRestored();
      refreshBackups();
    } catch (thrown: unknown) {
      setRestoreError(errorMessage(unwrapError(thrown)));
    } finally {
      setRestoring(false);
    }
  }

  async function handleRestoreWhole() {
    if (!selectedBackupId || !directory) return;
    setRestoring(true);
    setRestoreError(null);
    try {
      await restoreWhole(selectedBackupId, directory);
      setRestoreSuccess(true);
      setDryRun(null);
      setConfirmingWholeRestore(false);
      onRestored();
      refreshBackups();
    } catch (thrown: unknown) {
      setRestoreError(errorMessage(unwrapError(thrown)));
    } finally {
      setRestoring(false);
    }
  }

  return (
    <section className="backups-panel" aria-label={t("backups.title")}>
      <h3>{t("backups.title")}</h3>

      <div className="backup-directory-row">
        <span className="backup-directory-label">{t("backups.directory_label")}</span>
        <span className="backup-directory-path">{directory}</span>
        <button type="button" disabled={changingDirectory} onClick={() => void handleChangeDirectory()}>
          {changingDirectory ? t("backups.changing_directory_button") : t("backups.change_directory_button")}
        </button>
      </div>
      {directoryError ? (
        <p className="error" role="alert">
          {directoryError}
        </p>
      ) : null}

      <button type="button" disabled={creatingManual || !directory} onClick={() => void handleCreateManualBackup()}>
        {creatingManual ? t("backups.creating_manual_button") : t("backups.create_manual_button")}
      </button>
      {manualError ? (
        <p className="error" role="alert">
          {manualError}
        </p>
      ) : null}

      <div className="backup-kind-tabs" role="tablist" aria-label={t("backups.title")}>
        {BACKUP_KINDS.map((k) => (
          <button
            key={k}
            type="button"
            role="tab"
            aria-selected={k === kind}
            className={`backup-kind-tab${k === kind ? " selected" : ""}`}
            onClick={() => setKind(k)}
          >
            {t(`backups.kind_${k}`)}
          </button>
        ))}
      </div>

      {listError ? (
        <p className="error" role="alert">
          {listError}
        </p>
      ) : backups.length === 0 ? (
        <p className="hint">{t("backups.empty")}</p>
      ) : (
        <ul className="backup-list">
          {backups.map((backup) => (
            <li key={backup.id} className="backup-row">
              <span>{new Date(backup.created_unix_ms).toLocaleString()}</span>
              {backup.size_bytes !== undefined ? <span>{formatBytes(backup.size_bytes)}</span> : null}
              {backup.corrupt ? <span className="backup-corrupt-badge">{t("backups.corrupt_badge")}</span> : null}
              <button type="button" className="link-button" onClick={() => void handleVerify(backup.id)}>
                {t("backups.verify_button")}
              </button>
              <button type="button" className="link-button" onClick={() => handlePreview(backup.id)}>
                {t("backups.preview_button")}
              </button>
            </li>
          ))}
        </ul>
      )}

      {selectedBackupId ? (
        <div className="backup-preview">
          {previewError ? (
            <p className="error" role="alert">
              {previewError}
            </p>
          ) : preview === null ? (
            <p className="hint">{t("common.loading")}</p>
          ) : (
            <>
              <h4>{t("backups.preview_title")}</h4>
              {preview.note_titles.length === 0 ? (
                <p className="hint">{t("backups.preview_empty")}</p>
              ) : (
                <ul className="backup-preview-titles">
                  {preview.note_titles.map((title, index) => (
                    <li key={index}>{title}</li>
                  ))}
                </ul>
              )}

              {restoreSuccess ? <p className="backup-restore-success">{t("backups.restore_success")}</p> : null}
              {restoreError ? (
                <p className="error" role="alert">
                  {restoreError}
                </p>
              ) : null}

              {dryRun === null ? (
                <button type="button" disabled={planning} onClick={() => void handleStartRestore()}>
                  {planning ? t("backups.planning") : t("backups.start_restore_button")}
                </button>
              ) : (
                <div className="backup-dry-run">
                  <h4>{t("backups.dry_run_title")}</h4>
                  <p className="hint">
                    {t("backups.dry_run_required_storage")}: {formatBytes(dryRun.plan.required_storage_bytes)}
                  </p>
                  <ul className="backup-dry-run-entries">
                    {dryRun.plan.entries.map((entry) => (
                      <li key={entry.note_id}>
                        <label>
                          <input
                            type="checkbox"
                            checked={dryRun.selected.has(entry.note_id)}
                            onChange={() => toggleEntrySelected(entry.note_id)}
                          />
                          <span>{entry.title || t("shell.untitled_note")}</span>
                          <span className={`backup-dry-run-kind backup-dry-run-kind-${entry.kind}`}>
                            {t(`backups.dry_run_${entry.kind}`)}
                          </span>
                        </label>
                      </li>
                    ))}
                  </ul>

                  <button
                    type="button"
                    disabled={restoring || dryRun.selected.size === 0}
                    onClick={() => void handleRestoreSelected()}
                  >
                    {restoring ? t("backups.restoring") : t("backups.restore_selected_button")}
                  </button>

                  {confirmingWholeRestore ? (
                    <div className="backup-restore-whole-confirm">
                      <p>{t("backups.restore_whole_confirm")}</p>
                      <button type="button" disabled={restoring} onClick={() => void handleRestoreWhole()}>
                        {restoring ? t("backups.restoring") : t("backups.restore_whole_confirm_button")}
                      </button>
                      <button
                        type="button"
                        className="link-button"
                        disabled={restoring}
                        onClick={() => setConfirmingWholeRestore(false)}
                      >
                        {t("common.cancel")}
                      </button>
                    </div>
                  ) : (
                    <button type="button" className="link-button" onClick={() => setConfirmingWholeRestore(true)}>
                      {t("backups.restore_whole_button")}
                    </button>
                  )}
                </div>
              )}
              {planError ? (
                <p className="error" role="alert">
                  {planError}
                </p>
              ) : null}
            </>
          )}
          <button type="button" className="link-button" onClick={closePreview}>
            {t("backups.close_preview_button")}
          </button>
        </div>
      ) : null}
    </section>
  );
}

import { useState } from "react";

import {
  exportNotes,
  importBerestaArchive,
  importEvernoteArchive,
  pickExportDestination,
  pickImportSource,
  unwrapError,
} from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface ImportExportPanelProps {
  /** Called after a successful import, so the caller can reload its own
   * note/notebook/tag state - an import adds new local notes this panel
   * has no other way to tell the rest of the shell about. */
  onImported: () => void;
}

/**
 * ImportExportPanel covers task 5.7's confirmed plaintext export (behind
 * an explicit warning, per ExportNotes' own doc comment) and import of
 * both Beresta portable archives and Evernote .enex files, surfacing any
 * per-note warning the import could not fully represent.
 */
export function ImportExportPanel({ onImported }: ImportExportPanelProps) {
  const { t, errorMessage } = useI18n();

  const [confirmingExport, setConfirmingExport] = useState(false);
  const [exportBaseDir, setExportBaseDir] = useState("");
  const [exportFolderName, setExportFolderName] = useState("beresta-export");
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [exportManifest, setExportManifest] = useState<main.ExportManifestDTO | null>(null);

  const [importing, setImporting] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  const [importResult, setImportResult] = useState<main.ImportResultDTO | null>(null);

  async function handleChooseExportDestination() {
    setExportError(null);
    try {
      const picked = await pickExportDestination();
      if (picked) setExportBaseDir(picked);
    } catch (thrown: unknown) {
      setExportError(errorMessage(unwrapError(thrown)));
    }
  }

  async function handleConfirmExport() {
    const folderName = exportFolderName.trim();
    if (!exportBaseDir || !folderName) return;
    setExporting(true);
    setExportError(null);
    setExportManifest(null);
    try {
      const destDir = `${exportBaseDir}\\${folderName}`;
      const manifest = await exportNotes(destDir, []);
      setExportManifest(manifest);
      setConfirmingExport(false);
    } catch (thrown: unknown) {
      setExportError(errorMessage(unwrapError(thrown)));
    } finally {
      setExporting(false);
    }
  }

  async function handleImport(kind: "beresta" | "evernote") {
    setImportError(null);
    setImportResult(null);
    let source: string;
    try {
      source = await pickImportSource(kind);
    } catch (thrown: unknown) {
      setImportError(errorMessage(unwrapError(thrown)));
      return;
    }
    if (!source) return;
    setImporting(true);
    try {
      const result = kind === "beresta" ? await importBerestaArchive(source) : await importEvernoteArchive(source);
      setImportResult(result);
      onImported();
    } catch (thrown: unknown) {
      setImportError(errorMessage(unwrapError(thrown)));
    } finally {
      setImporting(false);
    }
  }

  return (
    <section className="import-export-panel">
      <div className="export-section">
        <h3>{t("export.title")}</h3>
        {confirmingExport ? (
          <div className="export-confirm">
            <p className="hint">{t("export.warning")}</p>
            <div className="export-destination-row">
              <span>{exportBaseDir || t("export.destination_label")}</span>
              <button type="button" onClick={() => void handleChooseExportDestination()}>
                {t("export.choose_destination_button")}
              </button>
            </div>
            <label>
              <span>{t("export.folder_label")}</span>
              <input
                type="text"
                value={exportFolderName}
                onChange={(event) => setExportFolderName(event.target.value)}
              />
            </label>
            <button
              type="button"
              disabled={exporting || !exportBaseDir || !exportFolderName.trim()}
              onClick={() => void handleConfirmExport()}
            >
              {exporting ? t("export.exporting_button") : t("export.confirm_button")}
            </button>
            <button
              type="button"
              className="link-button"
              disabled={exporting}
              onClick={() => setConfirmingExport(false)}
            >
              {t("common.cancel")}
            </button>
          </div>
        ) : (
          <button type="button" onClick={() => setConfirmingExport(true)}>
            {t("export.start_button")}
          </button>
        )}
        {exportError ? (
          <p className="error" role="alert">
            {exportError}
          </p>
        ) : null}
        {exportManifest ? <p className="export-success">{t("export.success")}</p> : null}
      </div>

      <div className="import-section">
        <h3>{t("import.title")}</h3>
        <div className="import-actions">
          <button type="button" disabled={importing} onClick={() => void handleImport("beresta")}>
            {importing ? t("import.importing_button") : t("import.beresta_button")}
          </button>
          <button type="button" disabled={importing} onClick={() => void handleImport("evernote")}>
            {importing ? t("import.importing_button") : t("import.evernote_button")}
          </button>
        </div>
        {importError ? (
          <p className="error" role="alert">
            {importError}
          </p>
        ) : null}
        {importResult ? (
          <div className="import-result">
            <p className="import-success">{t("import.success")}</p>
            {importResult.warnings.length > 0 ? (
              <>
                <p>{t("import.warnings_title")}</p>
                <ul className="import-warnings">
                  {importResult.warnings.map((warning, index) => (
                    <li key={index}>
                      <strong>{warning.note_title}</strong>: {warning.message}
                    </li>
                  ))}
                </ul>
              </>
            ) : null}
          </div>
        ) : null}
      </div>
    </section>
  );
}

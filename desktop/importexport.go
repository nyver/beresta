package main

import (
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/beresta-app/beresta/core/account"
)

// PickExportDestination opens the native directory picker for a new export
// destination. ExportNotes requires a directory that does not already
// exist, so the frontend should offer the chosen path plus a
// caller-supplied subfolder name rather than the picked directory itself.
func (a *App) PickExportDestination() (string, error) {
	ctx, err := a.runtimeContext()
	if err != nil {
		return "", mapError(err)
	}
	path, err := runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{Title: "Choose an export location"})
	if err != nil {
		return "", mapError(err)
	}
	return path, nil
}

// PickImportSource opens the native directory picker for an existing
// Beresta portable archive, or file picker for an Evernote .enex export.
func (a *App) PickImportSource(kind string) (string, error) {
	ctx, err := a.runtimeContext()
	if err != nil {
		return "", mapError(err)
	}
	switch kind {
	case "beresta":
		path, err := runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{Title: "Choose a Beresta export to import"})
		return path, mapError(err)
	case "evernote":
		path, err := runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{
			Title:   "Choose an Evernote .enex export to import",
			Filters: []runtime.FileFilter{{DisplayName: "Evernote export (*.enex)", Pattern: "*.enex"}},
		})
		return path, mapError(err)
	default:
		return "", mapError(&AppError{Code: ErrCodeInvalidInput, Message: "unknown import source kind"})
	}
}

// ExportManifestDTO is the JS-facing shape of an account.ExportManifest.
type ExportManifestDTO struct {
	Version    int   `json:"version"`
	ExportedMS int64 `json:"exported_unix_ms"`
	NoteCount  int   `json:"note_count"`
}

// ExportNotes writes noteIDs (or every note in the account's workspace,
// when noteIDs is empty) as plaintext Markdown, attachments, and a
// manifest.json to destDir. destDir must not already exist. This is the
// confirmed export action; the frontend is responsible for the required
// warning/confirmation before calling it.
func (a *App) ExportNotes(destDir string, noteIDs []string) (ExportManifestDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return ExportManifestDTO{}, mapError(err)
	}
	notes, err := parseIDs(noteIDs)
	if err != nil {
		return ExportManifestDTO{}, mapError(err)
	}
	manifest, err := acc.ExportNotes(a.requestContext(), workspaceID, destDir, notes, time.Now())
	if err != nil {
		return ExportManifestDTO{}, mapError(err)
	}
	return ExportManifestDTO{Version: manifest.Version, ExportedMS: manifest.ExportedUnixMS, NoteCount: len(manifest.Notes)}, nil
}

// ImportWarningDTO is one element an import could not fully represent.
type ImportWarningDTO struct {
	NoteTitle string `json:"note_title"`
	Message   string `json:"message"`
}

// ImportResultDTO reports what an import actually did.
type ImportResultDTO struct {
	NewNoteIDs []string           `json:"new_note_ids"`
	Warnings   []ImportWarningDTO `json:"warnings"`
}

func importResultDTO(r account.ImportResult) ImportResultDTO {
	warnings := make([]ImportWarningDTO, len(r.Warnings))
	for i, w := range r.Warnings {
		warnings[i] = ImportWarningDTO{NoteTitle: w.NoteTitle, Message: w.Message}
	}
	return ImportResultDTO{NewNoteIDs: idStrings(r.NewNoteIDs), Warnings: warnings}
}

// ImportBerestaArchive imports a portable archive written by ExportNotes.
func (a *App) ImportBerestaArchive(sourceDir string) (ImportResultDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return ImportResultDTO{}, mapError(err)
	}
	result, err := acc.ImportBerestaArchive(a.requestContext(), workspaceID, sourceDir)
	if err != nil {
		return importResultDTO(result), mapError(err)
	}
	return importResultDTO(result), nil
}

// ImportEvernoteArchive imports every note in an Evernote .enex export.
func (a *App) ImportEvernoteArchive(path string) (ImportResultDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return ImportResultDTO{}, mapError(err)
	}
	result, err := acc.ImportEvernoteArchive(a.requestContext(), workspaceID, path)
	if err != nil {
		return importResultDTO(result), mapError(err)
	}
	return importResultDTO(result), nil
}

// GCBlobCandidateDTO is one attachment blob eligible for garbage
// collection.
type GCBlobCandidateDTO struct {
	BlobID      string `json:"blob_id"`
	SizeBytes   uint64 `json:"size_bytes"`
	OrphanedMS  int64  `json:"orphaned_unix_ms"`
	InAnyBackup bool   `json:"in_any_backup"`
}

// GCNoteCandidateDTO is one tombstoned note eligible for permanent
// collection.
type GCNoteCandidateDTO struct {
	NoteID    string `json:"note_id"`
	Title     string `json:"title"`
	DeletedMS int64  `json:"deleted_unix_ms"`
}

// GCReportDTO is the result of RunGarbageCollection.
type GCReportDTO struct {
	Blobs              []GCBlobCandidateDTO `json:"blobs"`
	Notes              []GCNoteCandidateDTO `json:"notes"`
	BlobBytesReclaimed uint64               `json:"blob_bytes_reclaimed"`
	DryRun             bool                 `json:"dry_run"`
}

// RunGarbageCollection identifies (and, unless dryRun is true, permanently
// collects) every orphaned attachment blob and tombstoned note past the
// 30-day minimum retention window.
func (a *App) RunGarbageCollection(dryRun bool) (GCReportDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return GCReportDTO{}, mapError(err)
	}
	report, err := acc.RunGarbageCollection(a.requestContext(), time.Now(), dryRun)
	if err != nil {
		return GCReportDTO{}, mapError(err)
	}
	blobs := make([]GCBlobCandidateDTO, len(report.Blobs))
	for i, b := range report.Blobs {
		blobs[i] = GCBlobCandidateDTO{BlobID: blobIDString(b.BlobID), SizeBytes: b.SizeBytes, OrphanedMS: b.OrphanedUnixMS, InAnyBackup: b.InAnyBackup}
	}
	notes := make([]GCNoteCandidateDTO, len(report.Notes))
	for i, n := range report.Notes {
		notes[i] = GCNoteCandidateDTO{NoteID: idString(n.NoteID), Title: n.Title, DeletedMS: n.DeletedUnixMS}
	}
	return GCReportDTO{Blobs: blobs, Notes: notes, BlobBytesReclaimed: report.BlobBytesReclaimed, DryRun: report.DryRun}, nil
}

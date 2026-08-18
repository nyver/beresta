package account

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// ExportManifestVersion identifies the manifest.json schema ExportNotes
// writes.
const ExportManifestVersion = 1

// ExportedNote is one note's entry in an export manifest.
type ExportedNote struct {
	NoteID          model.ID `json:"note_id"`
	Title           string   `json:"title"`
	NotebookPath    []string `json:"notebook_path,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	MarkdownPath    string   `json:"markdown_path"`
	AttachmentPaths []string `json:"attachment_paths,omitempty"`
}

// ExportManifest is the portable manifest.json written beside an export's
// Markdown/attachment tree, representing everything ExportNotes wrote
// (notes-management spec: "a manifest that represents all exported data").
type ExportManifest struct {
	Version        int            `json:"version"`
	ExportedUnixMS int64          `json:"exported_unix_ms"`
	Notes          []ExportedNote `json:"notes"`
}

const exportManifestFile = "manifest.json"

// ExportNotes writes noteIDs (or every note in workspaceID, when noteIDs is
// empty) as plaintext Markdown files mirroring their notebook tree, their
// attachments alongside each note, and a manifest.json describing the
// whole export, to destDir. destDir must not already exist: ExportNotes
// stages the complete tree in a temporary sibling directory and renames it
// into place only once everything has been written successfully, so a
// failure partway through never leaves a partial export at destDir (it is
// removed instead).
//
// This is the confirmed export action itself; the caller is responsible
// for the notes-management spec's required explicit warning and
// confirmation before invoking it, since that is a UI concern this
// core-layer service does not own.
func (a *Account) ExportNotes(ctx context.Context, workspaceID model.ID, destDir string, noteIDs []model.ID, now time.Time) (ExportManifest, error) {
	db, entry, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return ExportManifest{}, err
	}
	if _, err := os.Stat(destDir); err == nil {
		return ExportManifest{}, fmt.Errorf("account: export destination already exists: %s", destDir)
	} else if !os.IsNotExist(err) {
		return ExportManifest{}, fmt.Errorf("account: check export destination: %w", err)
	}

	notes, err := selectSourceNotes(ctx, db, workspaceID, noteIDs)
	if err != nil {
		return ExportManifest{}, err
	}

	parent := filepath.Dir(destDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return ExportManifest{}, fmt.Errorf("account: create export parent directory: %w", err)
	}
	stagingDir, err := os.MkdirTemp(parent, ".export-*")
	if err != nil {
		return ExportManifest{}, fmt.Errorf("account: create export staging directory: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(stagingDir)
		}
	}()

	notebooks, err := store.ListNotebooks(ctx, db, workspaceID)
	if err != nil {
		return ExportManifest{}, err
	}
	notebooksByID := make(map[model.ID]store.Notebook, len(notebooks))
	for _, nb := range notebooks {
		notebooksByID[nb.ID] = nb
	}
	tags, err := store.ListTags(ctx, db, workspaceID)
	if err != nil {
		return ExportManifest{}, err
	}
	tagNamesByID := make(map[model.ID]string, len(tags))
	for _, tag := range tags {
		tagNamesByID[tag.ID] = tag.Name
	}

	usedPaths := make(map[string]int)
	manifest := ExportManifest{Version: ExportManifestVersion, ExportedUnixMS: now.UnixMilli(), Notes: make([]ExportedNote, 0, len(notes))}

	for _, note := range notes {
		notebookPath := notebookPathSegments(note.NotebookID.Value, notebooksByID)
		noteDir := filepath.Join(stagingDir, filepath.Join(sanitizedSegments(notebookPath)...))
		if err := os.MkdirAll(noteDir, 0o700); err != nil {
			return ExportManifest{}, fmt.Errorf("account: create export notebook directory: %w", err)
		}

		markdownName := uniqueExportName(usedPaths, filepath.Join(notebookPath...), sanitizeExportName(note.Title.Value), ".md")
		markdownRelPath := filepath.Join(filepath.Join(sanitizedSegments(notebookPath)...), markdownName)

		doc, err := loadNoteDocument(ctx, db, entry, workspaceID, note.ID)
		if err != nil {
			return ExportManifest{}, err
		}
		markdown, err := doc.Markdown(noteBodyRoot)
		doc.Close()
		if err != nil {
			return ExportManifest{}, err
		}
		if err := os.WriteFile(filepath.Join(stagingDir, markdownRelPath), []byte(markdown), 0o600); err != nil {
			return ExportManifest{}, fmt.Errorf("account: write exported note: %w", err)
		}

		exported := ExportedNote{
			NoteID:       note.ID,
			Title:        note.Title.Value,
			NotebookPath: notebookPath,
			MarkdownPath: filepath.ToSlash(markdownRelPath),
		}

		tagIDs, err := store.NoteTagIDs(ctx, db, note.ID)
		if err != nil {
			return ExportManifest{}, err
		}
		for _, tagID := range tagIDs {
			if name, ok := tagNamesByID[tagID]; ok {
				exported.Tags = append(exported.Tags, name)
			}
		}

		blobIDs, err := store.NoteAttachmentBlobIDs(ctx, db, note.ID)
		if err != nil {
			return ExportManifest{}, err
		}
		if len(blobIDs) > 0 {
			attachmentDir := filepath.Join(noteDir, "attachments", sanitizeExportName(note.Title.Value))
			if err := os.MkdirAll(attachmentDir, 0o700); err != nil {
				return ExportManifest{}, fmt.Errorf("account: create export attachment directory: %w", err)
			}
			attachmentUsedNames := make(map[string]int)
			for _, blobID := range blobIDs {
				f, err := os.CreateTemp(attachmentDir, "attachment-*")
				if err != nil {
					return ExportManifest{}, fmt.Errorf("account: stage exported attachment: %w", err)
				}
				displayName, _, err := readAttachmentFrom(ctx, db, a.blobs, entry.Key, workspaceID, blobID, f)
				if err != nil {
					f.Close()
					os.Remove(f.Name())
					return ExportManifest{}, fmt.Errorf("account: export attachment: %w", err)
				}
				if err := f.Close(); err != nil {
					return ExportManifest{}, fmt.Errorf("account: close exported attachment: %w", err)
				}
				finalName := uniqueExportName(attachmentUsedNames, "", sanitizeExportName(strings.TrimSuffix(displayName, filepath.Ext(displayName))), filepath.Ext(displayName))
				finalPath := filepath.Join(attachmentDir, finalName)
				if err := os.Rename(f.Name(), finalPath); err != nil {
					return ExportManifest{}, fmt.Errorf("account: publish exported attachment: %w", err)
				}
				relPath, err := filepath.Rel(stagingDir, finalPath)
				if err != nil {
					return ExportManifest{}, err
				}
				exported.AttachmentPaths = append(exported.AttachmentPaths, filepath.ToSlash(relPath))
			}
		}

		manifest.Notes = append(manifest.Notes, exported)
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ExportManifest{}, fmt.Errorf("account: encode export manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, exportManifestFile), manifestBytes, 0o600); err != nil {
		return ExportManifest{}, fmt.Errorf("account: write export manifest: %w", err)
	}

	if err := os.Rename(stagingDir, destDir); err != nil {
		return ExportManifest{}, fmt.Errorf("account: publish export: %w", err)
	}
	cleanup = false
	return manifest, nil
}

// notebookPathSegments returns notebookID's ancestor chain as display
// names, root first. A zero notebookID (filed at the workspace root)
// returns nil.
func notebookPathSegments(notebookID model.ID, byID map[model.ID]store.Notebook) []string {
	if notebookID.IsZero() {
		return nil
	}
	var segments []string
	current := notebookID
	for !current.IsZero() {
		nb, ok := byID[current]
		if !ok {
			break
		}
		segments = append([]string{nb.Name}, segments...)
		current = nb.ParentID
	}
	return segments
}

func sanitizedSegments(segments []string) []string {
	sanitized := make([]string, len(segments))
	for i, s := range segments {
		sanitized[i] = sanitizeExportName(s)
	}
	return sanitized
}

// exportNameDisallowed matches characters unsafe in a Windows (or portable
// cross-platform) file or directory name.
var exportNameDisallowed = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// sanitizeExportName turns an arbitrary title or notebook name into a safe
// path segment: disallowed characters become underscores, surrounding
// whitespace and trailing dots (invalid as a Windows path segment's last
// character) are trimmed, and an empty result falls back to "Untitled".
func sanitizeExportName(name string) string {
	sanitized := exportNameDisallowed.ReplaceAllString(name, "_")
	sanitized = strings.TrimRight(strings.TrimSpace(sanitized), ". ")
	if sanitized == "" {
		sanitized = "Untitled"
	}
	const maxSegmentBytes = 200
	if len(sanitized) > maxSegmentBytes {
		sanitized = sanitized[:maxSegmentBytes]
	}
	return sanitized
}

// uniqueExportName returns base+ext, or base " (n)"+ext for the smallest n
// that has not already been used under the same dir key in used.
func uniqueExportName(used map[string]int, dirKey, base, ext string) string {
	key := dirKey + "/" + base + ext
	count := used[key]
	used[key] = count + 1
	if count == 0 {
		return base + ext
	}
	return base + " (" + strconv.Itoa(count) + ")" + ext
}

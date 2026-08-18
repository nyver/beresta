package account

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

func TestExportNotesWritesMarkdownNotebookTreeAndManifest(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	notebook, err := created.CreateNotebook(ctx, workspaceID, model.Nil, "Work")
	if err != nil {
		t.Fatal(err)
	}
	tag, err := created.CreateTag(ctx, workspaceID, "urgent")
	if err != nil {
		t.Fatal(err)
	}
	note, err := created.CreateNote(ctx, workspaceID, notebook.ID, "Meeting notes")
	if err != nil {
		t.Fatal(err)
	}
	if err := created.SetNoteTag(ctx, workspaceID, note.ID, tag.ID, true); err != nil {
		t.Fatal(err)
	}
	commitInsert(t, created, workspaceID, note.ID, "hello export")
	if _, err := created.AddAttachment(ctx, workspaceID, note.ID, "agenda.txt", "text/plain", bytes.NewReader([]byte("agenda content"))); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(t.TempDir(), "export-out")
	manifest, err := created.ExportNotes(ctx, workspaceID, destDir, nil, time.Now())
	if err != nil {
		t.Fatalf("ExportNotes: %v", err)
	}
	if len(manifest.Notes) != 1 {
		t.Fatalf("manifest notes = %d, want 1", len(manifest.Notes))
	}
	entry := manifest.Notes[0]
	if entry.Title != "Meeting notes" || len(entry.NotebookPath) != 1 || entry.NotebookPath[0] != "Work" {
		t.Fatalf("manifest entry = %+v", entry)
	}
	if len(entry.Tags) != 1 || entry.Tags[0] != "urgent" {
		t.Fatalf("manifest tags = %v", entry.Tags)
	}
	if len(entry.AttachmentPaths) != 1 {
		t.Fatalf("manifest attachment paths = %v", entry.AttachmentPaths)
	}

	markdownBytes, err := os.ReadFile(filepath.Join(destDir, entry.MarkdownPath))
	if err != nil {
		t.Fatalf("read exported markdown: %v", err)
	}
	if !bytes.Contains(markdownBytes, []byte("hello export")) {
		t.Fatalf("exported markdown = %q, want it to contain the note body", markdownBytes)
	}

	attachmentBytes, err := os.ReadFile(filepath.Join(destDir, entry.AttachmentPaths[0]))
	if err != nil {
		t.Fatalf("read exported attachment: %v", err)
	}
	if string(attachmentBytes) != "agenda content" {
		t.Fatalf("exported attachment content = %q", attachmentBytes)
	}

	manifestOnDisk, err := os.ReadFile(filepath.Join(destDir, exportManifestFile))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var decoded ExportManifest
	if err := json.Unmarshal(manifestOnDisk, &decoded); err != nil {
		t.Fatalf("decode manifest.json: %v", err)
	}
	if len(decoded.Notes) != 1 {
		t.Fatalf("decoded manifest notes = %d, want 1", len(decoded.Notes))
	}
}

func TestExportNotesRejectsExistingDestination(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	if _, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled"); err != nil {
		t.Fatal(err)
	}

	destDir := t.TempDir() // already exists
	if _, err := created.ExportNotes(ctx, workspaceID, destDir, nil, time.Now()); err == nil {
		t.Fatal("expected an error for an already-existing export destination")
	}
}

func TestExportNotesHandlesDuplicateTitlesAndCleansUpOnFailure(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	first, err := created.CreateNote(ctx, workspaceID, model.Nil, "Same title")
	if err != nil {
		t.Fatal(err)
	}
	second, err := created.CreateNote(ctx, workspaceID, model.Nil, "Same title")
	if err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(t.TempDir(), "dup-export")
	manifest, err := created.ExportNotes(ctx, workspaceID, destDir, []model.ID{first.ID, second.ID}, time.Now())
	if err != nil {
		t.Fatalf("ExportNotes: %v", err)
	}
	if manifest.Notes[0].MarkdownPath == manifest.Notes[1].MarkdownPath {
		t.Fatalf("duplicate titles produced the same path: %q", manifest.Notes[0].MarkdownPath)
	}
	for _, entry := range manifest.Notes {
		if _, err := os.Stat(filepath.Join(destDir, entry.MarkdownPath)); err != nil {
			t.Fatalf("exported file missing: %v", err)
		}
	}
}

func TestExportNotesSanitizesUnsafeCharacters(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, `Q3 Plan: "priorities" / next?`)
	if err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(t.TempDir(), "sanitize-export")
	manifest, err := created.ExportNotes(ctx, workspaceID, destDir, []model.ID{note.ID}, time.Now())
	if err != nil {
		t.Fatalf("ExportNotes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, manifest.Notes[0].MarkdownPath)); err != nil {
		t.Fatalf("sanitized export file missing: %v", err)
	}
}

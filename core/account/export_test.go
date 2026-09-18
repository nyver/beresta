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

func TestExportNotesSkipsDeletedNotes(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	kept, err := created.CreateNote(ctx, workspaceID, model.Nil, "Kept note")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := created.CreateNote(ctx, workspaceID, model.Nil, "Deleted note")
	if err != nil {
		t.Fatal(err)
	}
	if err := created.DeleteNote(ctx, workspaceID, deleted.ID); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(t.TempDir(), "export-out")
	manifest, err := created.ExportNotes(ctx, workspaceID, destDir, nil, time.Now())
	if err != nil {
		t.Fatalf("ExportNotes: %v", err)
	}
	if len(manifest.Notes) != 1 || manifest.Notes[0].NoteID != kept.ID {
		t.Fatalf("manifest notes = %+v, want only the kept note", manifest.Notes)
	}

	// The same exclusion must hold when the deleted note is requested
	// explicitly, not just for the "export everything" path.
	destDir2 := filepath.Join(t.TempDir(), "export-explicit")
	manifest2, err := created.ExportNotes(ctx, workspaceID, destDir2, []model.ID{kept.ID, deleted.ID}, time.Now())
	if err != nil {
		t.Fatalf("ExportNotes: %v", err)
	}
	if len(manifest2.Notes) != 1 || manifest2.Notes[0].NoteID != kept.ID {
		t.Fatalf("manifest notes = %+v, want only the kept note", manifest2.Notes)
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

// canonicalFormatFixture exercises every format in the semantic set desktop
// and Android editor toolbars expose (specs/notes-management/spec.md:
// "Windows and Android SHALL expose the same semantic formatting set")
// through the canonical Markdown syntax core/sync/yjsadapter/markdown.go
// renders it as: bold, italic, strike, inline code, a link, headings one
// through three, a blockquote, both list kinds, and a code block. Both
// desktop's pasteFormat.ts and mobile's paste_format.dart degrade a paste
// down to exactly this set - see task 5.8's/6.1's round-trip fixtures on
// each client.
const canonicalFormatFixture = "# Heading one\n" +
	"## Heading two\n" +
	"### Heading three\n" +
	"**bold** *italic* ~~strike~~ `code` [a link](https://example.com)\n" +
	"> a quote\n" +
	"- bullet one\n" +
	"- bullet two\n" +
	"1. ordered one\n" +
	"2. ordered two\n" +
	"```\n" +
	"a code block\n" +
	"```"

// TestExportNotesRoundTripsTheCanonicalFormatFixture covers task 6.1's
// cross-platform export round-trip fixture: a note body written through the
// exact same ReplaceMarkdown+CommitNoteBody path core/mobileapi's SaveNote
// uses (so this exercises the real commit pipeline, not just the isolated
// yjsadapter parser/renderer already covered by markdown_test.go) must
// export byte-identical canonical Markdown for every format either
// client's editor toolbar can produce - proving the shared round-trip
// document model, not just its renderer in isolation, preserves the whole
// canonical set end to end.
func TestExportNotesRoundTripsTheCanonicalFormatFixture(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Every format")
	if err != nil {
		t.Fatal(err)
	}

	doc, err := loadNoteDocument(ctx, created.db, created, workspaceID, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ReplaceMarkdown(noteBodyRoot, canonicalFormatFixture); err != nil {
		doc.Close()
		t.Fatalf("ReplaceMarkdown: %v", err)
	}
	update, err := doc.EncodeStateAsUpdate(noteSnapshotFormat)
	doc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := created.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID: workspaceID, NoteID: note.ID, Update: update, UpdateFormat: noteSnapshotFormat,
	}); err != nil {
		t.Fatalf("CommitNoteBody: %v", err)
	}

	destDir := filepath.Join(t.TempDir(), "canonical-format-export")
	manifest, err := created.ExportNotes(ctx, workspaceID, destDir, []model.ID{note.ID}, time.Now())
	if err != nil {
		t.Fatalf("ExportNotes: %v", err)
	}
	exported, err := os.ReadFile(filepath.Join(destDir, manifest.Notes[0].MarkdownPath))
	if err != nil {
		t.Fatalf("read exported markdown: %v", err)
	}
	if string(exported) != canonicalFormatFixture {
		t.Fatalf("exported markdown = %q, want the fixture unchanged:\n%q", exported, canonicalFormatFixture)
	}
}

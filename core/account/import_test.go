package account

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

func TestImportBerestaArchiveRecreatesExportedContent(t *testing.T) {
	ctx := context.Background()
	source := createTestAccount(t)
	sourceWorkspace := defaultWorkspaceID(t, source)

	notebook, err := source.CreateNotebook(ctx, sourceWorkspace, model.Nil, "Recipes")
	if err != nil {
		t.Fatal(err)
	}
	tag, err := source.CreateTag(ctx, sourceWorkspace, "favorite")
	if err != nil {
		t.Fatal(err)
	}
	note, err := source.CreateNote(ctx, sourceWorkspace, notebook.ID, "Pancakes")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.SetNoteTag(ctx, sourceWorkspace, note.ID, tag.ID, true); err != nil {
		t.Fatal(err)
	}
	commitInsert(t, source, sourceWorkspace, note.ID, "flour, eggs, milk")
	if _, err := source.AddAttachment(ctx, sourceWorkspace, note.ID, "photo.txt", "text/plain", bytes.NewReader([]byte("stack of pancakes"))); err != nil {
		t.Fatal(err)
	}

	exportDir := filepath.Join(t.TempDir(), "export")
	if _, err := source.ExportNotes(ctx, sourceWorkspace, exportDir, nil, time.Now()); err != nil {
		t.Fatalf("ExportNotes: %v", err)
	}

	target := createTestAccount(t)
	targetWorkspace := defaultWorkspaceID(t, target)
	result, err := target.ImportBerestaArchive(ctx, targetWorkspace, exportDir)
	if err != nil {
		t.Fatalf("ImportBerestaArchive: %v", err)
	}
	if len(result.NewNoteIDs) != 1 {
		t.Fatalf("NewNoteIDs = %v, want 1", result.NewNoteIDs)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected a formatting-simplification warning")
	}

	imported, err := target.GetNote(ctx, result.NewNoteIDs[0])
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if imported.Title.Value != "Pancakes" || imported.NotebookID.Value.IsZero() {
		t.Fatalf("imported note = %+v", imported)
	}

	notebooks, err := target.ListNotebooks(ctx, targetWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, nb := range notebooks {
		if nb.ID == imported.NotebookID.Value && nb.Name == "Recipes" {
			found = true
		}
	}
	if !found {
		t.Fatal("imported note's notebook was not recreated as Recipes")
	}

	tagIDs, err := store.NoteTagIDs(ctx, target.db, imported.ID)
	if err != nil || len(tagIDs) != 1 {
		t.Fatalf("tag IDs = %v, err = %v", tagIDs, err)
	}

	doc, err := loadNoteDocument(ctx, target.db, target, targetWorkspace, imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	text, err := doc.Text(noteBodyRoot)
	doc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "flour, eggs, milk") {
		t.Fatalf("imported body = %q", text)
	}
}

func TestImportEvernoteArchiveParsesNotesTagsAndResources(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	resourceData := "aGVsbG8gcmVzb3VyY2U=" // base64("hello resource")
	enex := `<?xml version="1.0" encoding="UTF-8"?>
<en-export>
  <note>
    <title>Trip ideas</title>
    <content><![CDATA[<?xml version="1.0" encoding="UTF-8"?><en-note><div>Visit the <b>museum</b></div><div>and the park</div></en-note>]]></content>
    <tag>travel</tag>
    <resource>
      <data encoding="base64">` + resourceData + `</data>
      <mime>text/plain</mime>
      <resource-attributes>
        <file-name>note.txt</file-name>
      </resource-attributes>
    </resource>
  </note>
</en-export>`

	path := filepath.Join(t.TempDir(), "export.enex")
	if err := os.WriteFile(path, []byte(enex), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := created.ImportEvernoteArchive(ctx, workspaceID, path)
	if err != nil {
		t.Fatalf("ImportEvernoteArchive: %v", err)
	}
	if len(result.NewNoteIDs) != 1 {
		t.Fatalf("NewNoteIDs = %v, want 1", result.NewNoteIDs)
	}
	note, err := created.GetNote(ctx, result.NewNoteIDs[0])
	if err != nil || note.Title.Value != "Trip ideas" {
		t.Fatalf("note = %+v, err = %v", note, err)
	}

	doc, err := loadNoteDocument(ctx, created.db, created, workspaceID, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	text, err := doc.Text(noteBodyRoot)
	doc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Visit the museum") || !strings.Contains(text, "and the park") {
		t.Fatalf("imported ENML text = %q", text)
	}

	tagIDs, err := store.NoteTagIDs(ctx, created.db, note.ID)
	if err != nil || len(tagIDs) != 1 {
		t.Fatalf("tag IDs = %v, err = %v", tagIDs, err)
	}

	var out bytes.Buffer
	blobIDs, err := store.NoteAttachmentBlobIDs(ctx, created.db, note.ID)
	if err != nil || len(blobIDs) != 1 {
		t.Fatalf("blob IDs = %v, err = %v", blobIDs, err)
	}
	name, _, err := created.ReadAttachment(ctx, workspaceID, blobIDs[0], &out)
	if err != nil || name != "note.txt" || out.String() != "hello resource" {
		t.Fatalf("resource name=%q content=%q err=%v", name, out.String(), err)
	}
}

func TestEnmlToPlainTextStripsTagsAndPreservesLineBreaks(t *testing.T) {
	got := enmlToPlainText(`<div>Hello <b>world</b></div><div>Line two</div>`)
	want := "Hello world\nLine two"
	if got != want {
		t.Fatalf("enmlToPlainText = %q, want %q", got, want)
	}
}

// TestImportBerestaArchiveAttachmentWarningOmitsRawFilesystemDetail covers
// task 4.4's "Actionable and safe error presentation" requirement: a
// missing/unreadable exported attachment must still produce a warning
// naming which attachment failed, but never the underlying os.Open error
// text, which typically carries a full filesystem path (and, on a real
// machine, the OS username embedded in that path) - not something that
// belongs in primary UI text.
func TestImportBerestaArchiveAttachmentWarningOmitsRawFilesystemDetail(t *testing.T) {
	ctx := context.Background()
	source := createTestAccount(t)
	sourceWorkspace := defaultWorkspaceID(t, source)

	note, err := source.CreateNote(ctx, sourceWorkspace, model.Nil, "Trip photos")
	if err != nil {
		t.Fatal(err)
	}
	commitInsert(t, source, sourceWorkspace, note.ID, "see attached")
	if _, err := source.AddAttachment(ctx, sourceWorkspace, note.ID, "photo.txt", "text/plain", bytes.NewReader([]byte("stack of pancakes"))); err != nil {
		t.Fatal(err)
	}

	exportDir := filepath.Join(t.TempDir(), "export")
	if _, err := source.ExportNotes(ctx, sourceWorkspace, exportDir, nil, time.Now()); err != nil {
		t.Fatalf("ExportNotes: %v", err)
	}

	// Remove the exported attachment file itself (but keep the manifest
	// referencing it), so import's os.Open on it fails with a real,
	// path-carrying filesystem error - exactly the shape of error that
	// must never reach ImportWarning.Message.
	removed := false
	if err := filepath.Walk(exportDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		for _, component := range strings.Split(filepath.ToSlash(path), "/") {
			if component == "attachments" {
				removed = true
				return os.Remove(path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("did not find the exported attachment file to remove")
	}

	target := createTestAccount(t)
	targetWorkspace := defaultWorkspaceID(t, target)
	result, err := target.ImportBerestaArchive(ctx, targetWorkspace, exportDir)
	if err != nil {
		t.Fatalf("ImportBerestaArchive: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected a warning for the missing attachment")
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning.Message, exportDir) {
			t.Fatalf("ImportWarning.Message leaked the export directory path: %q", warning.Message)
		}
		if strings.Contains(strings.ToLower(warning.Message), "no such file") ||
			strings.Contains(warning.Message, "cannot find the file") {
			t.Fatalf("ImportWarning.Message leaked raw OS error text: %q", warning.Message)
		}
	}
}

func TestAttachmentMediaTypeFromExtension(t *testing.T) {
	cases := map[string]string{
		"photo.png":    "image/png",
		"photo.PNG":    "image/png",
		"photo.jpg":    "image/jpeg",
		"photo.jpeg":   "image/jpeg",
		"anim.gif":     "image/gif",
		"doc.pdf":      "application/pdf",
		"notes.txt":    "text/plain",
		"archive.dat":  "application/octet-stream",
		"no-extension": "application/octet-stream",
	}
	for name, want := range cases {
		if got := attachmentMediaTypeFromExtension(name); got != want {
			t.Errorf("attachmentMediaTypeFromExtension(%q) = %q, want %q", name, got, want)
		}
	}
}

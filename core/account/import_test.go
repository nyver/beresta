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

	doc, err := loadNoteDocument(ctx, target.db, target.workspaceKeys[targetWorkspace], targetWorkspace, imported.ID)
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

	doc, err := loadNoteDocument(ctx, created.db, created.workspaceKeys[workspaceID], workspaceID, note.ID)
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

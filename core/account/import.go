package account

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

// ImportWarning reports one element an import could not fully represent
// (notes-management spec: "reports any elements that could not be
// represented"), attributed to the note it came from.
type ImportWarning struct {
	NoteTitle string
	Message   string
}

// ImportResult reports what an import actually did.
type ImportResult struct {
	NewNoteIDs []model.ID
	Warnings   []ImportWarning
}

// ImportBerestaArchive imports a portable archive written by ExportNotes:
// every note in its manifest.json, with its notebook path, tags, plain-text
// body, and attachments recreated as new local notes and operations. Rich
// text formatting is not recovered: ExportNotes writes canonical Markdown
// text, and there is no Markdown-to-CRDT parser in this codebase, so the
// Markdown text is imported as plain text (matching the same accepted
// simplification RestoreRevision's rollback makes) and reported as a
// warning per note.
func (a *Account) ImportBerestaArchive(ctx context.Context, workspaceID model.ID, sourceDir string) (ImportResult, error) {
	manifestBytes, err := os.ReadFile(filepath.Join(sourceDir, exportManifestFile))
	if err != nil {
		return ImportResult{}, fmt.Errorf("account: read import manifest: %w", err)
	}
	var manifest ExportManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return ImportResult{}, fmt.Errorf("account: decode import manifest: %w", err)
	}

	result := ImportResult{NewNoteIDs: make([]model.ID, 0, len(manifest.Notes))}
	notebookMapping := make(map[string]model.ID)
	tagMapping := make(map[string]model.ID)

	for _, entry := range manifest.Notes {
		markdownBytes, err := os.ReadFile(filepath.Join(sourceDir, filepath.FromSlash(entry.MarkdownPath)))
		if err != nil {
			return result, fmt.Errorf("account: read imported note %q: %w", entry.Title, err)
		}

		notebookID := model.Nil
		if len(entry.NotebookPath) > 0 {
			notebookID, err = a.findOrCreateNotebookPath(ctx, workspaceID, entry.NotebookPath, notebookMapping)
			if err != nil {
				return result, err
			}
		}

		newNote, err := a.CreateNote(ctx, workspaceID, notebookID, entry.Title)
		if err != nil {
			return result, err
		}
		result.NewNoteIDs = append(result.NewNoteIDs, newNote.ID)
		result.Warnings = append(result.Warnings, ImportWarning{
			NoteTitle: entry.Title,
			Message:   "rich text formatting could not be recovered from exported Markdown; imported as plain text",
		})

		if err := a.importPlainTextBody(ctx, workspaceID, newNote.ID, string(markdownBytes)); err != nil {
			return result, err
		}

		for _, tagName := range entry.Tags {
			tagID, err := a.findOrCreateTagByName(ctx, workspaceID, tagName, tagMapping)
			if err != nil {
				return result, err
			}
			if err := a.SetNoteTag(ctx, workspaceID, newNote.ID, tagID, true); err != nil {
				return result, err
			}
		}

		for _, attachmentPath := range entry.AttachmentPaths {
			if err := a.importAttachmentFile(ctx, workspaceID, newNote.ID, filepath.Join(sourceDir, filepath.FromSlash(attachmentPath))); err != nil {
				result.Warnings = append(result.Warnings, ImportWarning{
					NoteTitle: entry.Title,
					Message:   fmt.Sprintf("attachment %q could not be imported: %v", filepath.Base(attachmentPath), err),
				})
			}
		}
	}
	return result, nil
}

// importPlainTextBody commits text as a note's complete body, via a fresh
// document (the established idiom throughout this package for "produce an
// update representing content X": build it on an empty document, then feed
// its full encoded state to CommitNoteBody).
func (a *Account) importPlainTextBody(ctx context.Context, workspaceID, noteID model.ID, text string) error {
	if text == "" {
		return nil
	}
	doc := yjsadapter.New()
	if err := doc.Insert(noteBodyRoot, 0, text, nil); err != nil {
		doc.Close()
		return fmt.Errorf("account: build imported note body: %w", err)
	}
	update, err := doc.EncodeStateAsUpdate(noteSnapshotFormat)
	doc.Close()
	if err != nil {
		return err
	}
	return a.CommitNoteBody(ctx, NoteBodyCommand{WorkspaceID: workspaceID, NoteID: noteID, Update: update, UpdateFormat: noteSnapshotFormat})
}

func (a *Account) importAttachmentFile(ctx context.Context, workspaceID, noteID model.ID, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = a.AddAttachment(ctx, workspaceID, noteID, filepath.Base(path), attachmentMediaTypeFromExtension(path), f)
	return err
}

func (a *Account) findOrCreateNotebookPath(ctx context.Context, workspaceID model.ID, segments []string, mapping map[string]model.ID) (model.ID, error) {
	parentID := model.Nil
	pathKey := ""
	for _, name := range segments {
		pathKey += "/" + name
		if resolved, ok := mapping[pathKey]; ok {
			parentID = resolved
			continue
		}
		local, err := findOrCreateLocalNotebook(ctx, a, workspaceID, parentID, name)
		if err != nil {
			return model.Nil, err
		}
		mapping[pathKey] = local
		parentID = local
	}
	return parentID, nil
}

func (a *Account) findOrCreateTagByName(ctx context.Context, workspaceID model.ID, name string, mapping map[string]model.ID) (model.ID, error) {
	if resolved, ok := mapping[name]; ok {
		return resolved, nil
	}
	tags, err := a.ListTags(ctx, workspaceID)
	if err != nil {
		return model.Nil, err
	}
	for _, tag := range tags {
		if !tag.Deleted && tag.Name == name {
			mapping[name] = tag.ID
			return tag.ID, nil
		}
	}
	created, err := a.CreateTag(ctx, workspaceID, name)
	if err != nil {
		return model.Nil, err
	}
	mapping[name] = created.ID
	return created.ID, nil
}

func attachmentMediaTypeFromExtension(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// --- Evernote .enex import ---

type enexExport struct {
	XMLName xml.Name   `xml:"en-export"`
	Notes   []enexNote `xml:"note"`
}

type enexNote struct {
	Title     string         `xml:"title"`
	Content   string         `xml:"content"`
	Tags      []string       `xml:"tag"`
	Resources []enexResource `xml:"resource"`
}

type enexResource struct {
	Data               string                 `xml:"data"`
	Mime               string                 `xml:"mime"`
	ResourceAttributes enexResourceAttributes `xml:"resource-attributes"`
}

type enexResourceAttributes struct {
	FileName string `xml:"file-name"`
}

// ImportEvernoteArchive imports every note in an Evernote .enex export at
// path: title, tags, resources (as attachments), and plain-text content —
// ENML (Evernote's XHTML-like rich text dialect) has no representation in
// this codebase's CRDT rich-text model, so it is flattened to plain text
// and reported as a warning per note, matching ImportBerestaArchive's same
// simplification. An .enex element this function does not recognize at all
// (for example note-attributes like geolocation or reminders, which are
// simply not read) is not separately warned about per occurrence; the
// per-note "content simplified" warning covers the whole note attribute
// set exported by Evernote that this importer does not carry over.
func (a *Account) ImportEvernoteArchive(ctx context.Context, workspaceID model.ID, path string) (ImportResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("account: read .enex file: %w", err)
	}
	var export enexExport
	if err := xml.Unmarshal(data, &export); err != nil {
		return ImportResult{}, fmt.Errorf("account: parse .enex file: %w", err)
	}

	result := ImportResult{NewNoteIDs: make([]model.ID, 0, len(export.Notes))}
	tagMapping := make(map[string]model.ID)

	for _, note := range export.Notes {
		title := note.Title
		if title == "" {
			title = "Untitled"
		}
		newNote, err := a.CreateNote(ctx, workspaceID, model.Nil, title)
		if err != nil {
			return result, err
		}
		result.NewNoteIDs = append(result.NewNoteIDs, newNote.ID)
		result.Warnings = append(result.Warnings, ImportWarning{
			NoteTitle: title,
			Message:   "Evernote rich text (ENML) has no equivalent in this app and was flattened to plain text",
		})

		plainText := enmlToPlainText(note.Content)
		if err := a.importPlainTextBody(ctx, workspaceID, newNote.ID, plainText); err != nil {
			return result, err
		}

		for _, tagName := range note.Tags {
			if tagName == "" {
				continue
			}
			tagID, err := a.findOrCreateTagByName(ctx, workspaceID, tagName, tagMapping)
			if err != nil {
				return result, err
			}
			if err := a.SetNoteTag(ctx, workspaceID, newNote.ID, tagID, true); err != nil {
				return result, err
			}
		}

		for i, res := range note.Resources {
			if err := a.importEnexResource(ctx, workspaceID, newNote.ID, i, res); err != nil {
				result.Warnings = append(result.Warnings, ImportWarning{
					NoteTitle: title,
					Message:   fmt.Sprintf("resource %d could not be imported: %v", i+1, err),
				})
			}
		}
	}
	return result, nil
}

func (a *Account) importEnexResource(ctx context.Context, workspaceID, noteID model.ID, index int, res enexResource) error {
	raw := strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(res.Data)
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("decode resource data: %w", err)
	}
	displayName := res.ResourceAttributes.FileName
	if displayName == "" {
		displayName = fmt.Sprintf("attachment-%d", index+1)
	}
	mediaType := res.Mime
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	_, err = a.AddAttachment(ctx, workspaceID, noteID, displayName, mediaType, bytes.NewReader(decoded))
	return err
}

// enmlBlockTags become a newline; every other tag is dropped outright.
var enmlTag = regexp.MustCompile(`(?is)<[^>]*>`)
var enmlBlockBoundary = regexp.MustCompile(`(?is)</?(div|p|br|li|h[1-6])[^>]*>`)

// enmlToPlainText flattens Evernote's ENML rich text into plain text: block
// boundaries become newlines, every other tag is stripped, and HTML
// entities are unescaped. It does not attempt to preserve any formatting,
// list structure, or embedded resource references.
func enmlToPlainText(enml string) string {
	withBreaks := enmlBlockBoundary.ReplaceAllString(enml, "\n")
	stripped := enmlTag.ReplaceAllString(withBreaks, "")
	unescaped := html.UnescapeString(stripped)
	lines := strings.Split(unescaped, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, "\n")
}

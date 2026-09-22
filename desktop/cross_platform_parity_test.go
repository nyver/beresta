package main

// task 11.6: cross-platform parity automation. Every existing two-actor
// sync test in this repo (TestWorkspaceSharingAcrossTwoAccounts here,
// core/mobileapi's TestServiceWorkspaceSharingAcrossTwoAccounts) pairs two
// instances of the *same* client type - two desktop.Apps, or two
// mobileapi.Services - so each proves same-platform convergence in
// isolation, never that a real desktop actor and a real mobile actor
// converge on the same synced state. desktop (package main) and
// core/mobileapi share one Go module, so nothing prevents a test file
// living here from driving both directly; this file is the first to do so,
// covering all eight areas task 11.6 names against one shared owner
// (desktop)/joiner (mobile) pair, mirroring the existing mega-tests'
// sequential-narrative structure rather than eight separate expensive E2E
// setups.
//
// Where a merge outcome's exact shape isn't something this sandbox can
// verify at runtime (no C compiler for SQLCipher - the same limitation
// documented since task 7.8), assertions deliberately check safe,
// well-understood properties (both actors converge to byte-identical
// content; no concurrently-written content is lost) rather than a
// predicted exact ordering, since Yjs's CRDT merge guarantees the former
// unconditionally but the latter depends on internal tie-breaking this
// test has no way to confirm without executing it.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/mobileapi"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
	"github.com/beresta-app/beresta/server"
)

// mobileWorkspaceSummary mirrors core/mobileapi/sharing.go's unexported
// workspaceSummary JSON shape, which this package cannot import directly.
type mobileWorkspaceSummary struct {
	WorkspaceID string `json:"workspace_id"`
	Role        string `json:"role"`
	Active      bool   `json:"active"`
	MemberCount int    `json:"member_count,omitempty"`
}

func decodeMobileJSON[T any](t *testing.T, raw string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode mobile JSON %q: %v", raw, err)
	}
	return value
}

// connectMobileE2EActor mirrors connectDesktopE2EActor above and
// core/mobileapi/sharing_test.go's newConnectedMobileService, against the
// same real *server.Runtime a desktop actor in the same test also connects
// to.
func connectMobileE2EActor(t *testing.T, runtime *server.Runtime, baseURL, name string) *mobileapi.Service {
	t.Helper()
	dir, err := os.MkdirTemp("", "beresta-mobile-db-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dbPath := filepath.Join(dir, "beresta.db")
	service, err := mobileapi.NewService(bytesRepeat(0xAB, 32))
	if err != nil {
		t.Fatalf("mobileapi.NewService(%s): %v", name, err)
	}
	t.Cleanup(service.Close)
	if _, err := service.CreateAccount("create-"+name, dbPath, "correct horse battery staple "+name); err != nil {
		t.Fatalf("mobile CreateAccount(%s): %v", name, err)
	}
	invite, err := runtime.Storage.CreateInvite(context.Background(), name, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("CreateInvite(%s): %v", name, err)
	}
	config, err := json.Marshal(map[string]string{
		"url": baseURL, "invite_code": invite.Code, "fingerprint": runtime.TLSIdentity.Fingerprint,
		"security_mode": "pinned", "device_name": name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConnectServer("connect-"+name, string(config)); err != nil {
		t.Fatalf("mobile ConnectServer(%s): %v", name, err)
	}
	return service
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// yjsUpdateFromScratch mirrors notedocument_test.go's encodedNoteUpdate,
// building a Yjs update from a fresh, independent document - the shape of
// a genuinely concurrent edit with no shared history, rather than one
// derived from the target note's own current state.
func yjsUpdateFromScratch(t *testing.T, markdown string) (base64Update, format string) {
	t.Helper()
	doc := yjsadapter.New()
	defer doc.Close()
	if err := doc.ReplaceMarkdown("body", markdown); err != nil {
		t.Fatalf("build update: %v", err)
	}
	update, err := doc.EncodeStateAsUpdate(yjsadapter.FormatV1)
	if err != nil {
		t.Fatalf("encode update: %v", err)
	}
	return base64.StdEncoding.EncodeToString(update), "v1"
}

func desktopMarkdown(t *testing.T, a *App, noteID string) string {
	t.Helper()
	doc, err := a.GetNoteDocument(noteID)
	if err != nil {
		t.Fatalf("GetNoteDocument(%s): %v", noteID, err)
	}
	raw, err := base64.StdEncoding.DecodeString(doc.UpdateBase64)
	if err != nil {
		t.Fatal(err)
	}
	format := yjsadapter.FormatV1
	if doc.Format == "v2" {
		format = yjsadapter.FormatV2
	}
	loaded := yjsadapter.New()
	defer loaded.Close()
	if err := loaded.ApplyUpdate(format, raw); err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	md, err := loaded.Markdown("body")
	if err != nil {
		t.Fatalf("Markdown(): %v", err)
	}
	return md
}

// TestCrossPlatformParityAcrossDesktopAndMobile drives a real desktop.App
// owner and a real core/mobileapi.Service joiner, sharing one workspace on
// one real server, through every area task 11.6 names: formatting,
// attachments, tags/notebooks, revisions, simultaneous offline edits,
// delete/move conflicts, reconnect, and workspace switch.
func TestCrossPlatformParityAcrossDesktopAndMobile(t *testing.T) {
	runtime, baseURL := startDesktopE2EServer(t)

	desktopActor := newTestApp(t)
	connectDesktopE2EActor(t, desktopActor, runtime, baseURL, "desktop-owner")
	mobileActor := connectMobileE2EActor(t, runtime, baseURL, "mobile-joiner")

	// --- Establish one shared workspace, owned by the desktop actor. ---
	identityJSON, err := mobileActor.ExportIdentity("export-identity")
	if err != nil {
		t.Fatalf("mobile ExportIdentity: %v", err)
	}
	identityCode := decodeMobileJSON[map[string]string](t, identityJSON)["identity_code"]
	grantCode, err := desktopActor.ShareWorkspace(identityCode)
	if err != nil {
		t.Fatalf("desktop ShareWorkspace: %v", err)
	}
	acceptJSON, err := mobileActor.AcceptWorkspaceGrant("accept", grantCode)
	if err != nil {
		t.Fatalf("mobile AcceptWorkspaceGrant: %v", err)
	}
	shared := decodeMobileJSON[mobileWorkspaceSummary](t, acceptJSON)
	if shared.Role != "member" || !shared.Active {
		t.Fatalf("mobile accept summary = %+v, want an active member", shared)
	}
	sharedWorkspaceID := shared.WorkspaceID

	syncBoth := func(t *testing.T) {
		t.Helper()
		if err := desktopActor.SyncNow(); err != nil {
			t.Fatalf("desktop SyncNow: %v", err)
		}
		if err := mobileActor.SyncNow(); err != nil {
			t.Fatalf("mobile SyncNow: %v", err)
		}
	}
	waitUntilMobileSeesNote := func(t *testing.T, title string) string {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			syncBoth(t)
			notesJSON, err := mobileActor.ListNotes("list")
			if err != nil {
				t.Fatalf("mobile ListNotes: %v", err)
			}
			for _, note := range decodeMobileJSON[[]map[string]any](t, notesJSON) {
				if note["title"] == title {
					return note["id"].(string)
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("mobile actor never saw note %q after syncing", title)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}

	// --- 1. Formatting: desktop commits Markdown formatting, mobile must
	// read back the identical Markdown after sync. ---
	note, err := desktopActor.CreateNote("", "Formatting parity")
	if err != nil {
		t.Fatalf("desktop CreateNote: %v", err)
	}
	const formattedMarkdown = "**bold** and _italic_ and `code`"
	update, format := yjsUpdateFromScratch(t, formattedMarkdown)
	if err := desktopActor.CommitNoteBody(CommitNoteBodyRequest{NoteID: note.ID, UpdateBase64: update, UpdateFormat: format}); err != nil {
		t.Fatalf("desktop CommitNoteBody: %v", err)
	}
	mobileNoteID := waitUntilMobileSeesNote(t, "Formatting parity")
	mobileGetJSON, err := mobileActor.GetNote("get-formatting", mobileNoteID)
	if err != nil {
		t.Fatalf("mobile GetNote: %v", err)
	}
	mobileBody := decodeMobileJSON[map[string]any](t, mobileGetJSON)["body"].(string)
	if strings.TrimSpace(mobileBody) != formattedMarkdown {
		t.Fatalf("mobile body = %q, want %q (desktop's formatted Markdown)", mobileBody, formattedMarkdown)
	}
	if got := strings.TrimSpace(desktopMarkdown(t, desktopActor, note.ID)); got != formattedMarkdown {
		t.Fatalf("desktop's own re-decoded Markdown = %q, want %q", got, formattedMarkdown)
	}

	// --- 2. Attachments: desktop adds one, mobile must see identical
	// bytes and metadata after sync. ---
	const attachmentContent = "beresta-cross-platform-attachment-bytes"
	attachmentDTO, err := desktopActor.AddAttachmentFromBytes(note.ID, "notes.txt", "text/plain", base64.StdEncoding.EncodeToString([]byte(attachmentContent)))
	if err != nil {
		t.Fatalf("desktop AddAttachmentFromBytes: %v", err)
	}
	syncBoth(t)
	deadline := time.Now().Add(20 * time.Second)
	var mobileAttachments []map[string]any
	for {
		syncBoth(t)
		attachmentsJSON, err := mobileActor.ListNoteAttachments("list-attachments", mobileNoteID)
		if err != nil {
			t.Fatalf("mobile ListNoteAttachments: %v", err)
		}
		mobileAttachments = decodeMobileJSON[[]map[string]any](t, attachmentsJSON)
		if len(mobileAttachments) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(mobileAttachments) != 1 {
		t.Fatalf("mobile attachments = %+v, want exactly 1", mobileAttachments)
	}
	if mobileAttachments[0]["blob_id"] != attachmentDTO.BlobID {
		t.Fatalf("mobile attachment blob_id = %v, want %q", mobileAttachments[0]["blob_id"], attachmentDTO.BlobID)
	}
	mobileAttachmentBytes, err := mobileActor.ReadAttachmentData("read-attachment", attachmentDTO.BlobID)
	if err != nil {
		t.Fatalf("mobile ReadAttachmentData: %v", err)
	}
	if string(mobileAttachmentBytes) != attachmentContent {
		t.Fatalf("mobile attachment content = %q, want %q", mobileAttachmentBytes, attachmentContent)
	}

	// --- 3. Tags/notebooks: desktop creates and assigns both, mobile
	// must see the identical assignment after sync. ---
	notebook, err := desktopActor.CreateNotebook("", "Shared notebook")
	if err != nil {
		t.Fatalf("desktop CreateNotebook: %v", err)
	}
	tag, err := desktopActor.CreateTag("shared-tag")
	if err != nil {
		t.Fatalf("desktop CreateTag: %v", err)
	}
	if err := desktopActor.SetNoteNotebook(note.ID, notebook.ID); err != nil {
		t.Fatalf("desktop SetNoteNotebook: %v", err)
	}
	if err := desktopActor.SetNoteTag(note.ID, tag.ID, true); err != nil {
		t.Fatalf("desktop SetNoteTag: %v", err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		syncBoth(t)
		mobileGetJSON, err = mobileActor.GetNote("get-tagged", mobileNoteID)
		if err != nil {
			t.Fatalf("mobile GetNote: %v", err)
		}
		mobileNoteView := decodeMobileJSON[map[string]any](t, mobileGetJSON)["note"].(map[string]any)
		if mobileNoteView["notebook_id"] == notebook.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mobile note never observed the desktop-assigned notebook: %+v", mobileNoteView)
		}
		time.Sleep(25 * time.Millisecond)
	}
	mobileTagsJSON, err := mobileActor.ListNoteTags("list-note-tags", mobileNoteID)
	if err != nil {
		t.Fatalf("mobile ListNoteTags: %v", err)
	}
	mobileTagIDs := decodeMobileJSON[[]string](t, mobileTagsJSON)
	found := false
	for _, id := range mobileTagIDs {
		if id == tag.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("mobile note tags = %v, want to include desktop-assigned tag %q", mobileTagIDs, tag.ID)
	}

	// --- 4. Revisions: desktop's commits accumulate delta revisions;
	// mobile must see the same count after sync. ---
	for i := 0; i < 2; i++ {
		update, format := yjsUpdateFromScratch(t, formattedMarkdown+" revision "+string(rune('A'+i)))
		if err := desktopActor.CommitNoteBody(CommitNoteBodyRequest{NoteID: note.ID, UpdateBase64: update, UpdateFormat: format}); err != nil {
			t.Fatalf("desktop CommitNoteBody(revision %d): %v", i, err)
		}
	}
	desktopRevisions, err := desktopActor.ListRevisions(note.ID)
	if err != nil {
		t.Fatalf("desktop ListRevisions: %v", err)
	}
	deadline = time.Now().Add(20 * time.Second)
	var mobileRevisions []map[string]any
	for {
		syncBoth(t)
		revisionsJSON, err := mobileActor.ListRevisions("list-revisions", mobileNoteID)
		if err != nil {
			t.Fatalf("mobile ListRevisions: %v", err)
		}
		mobileRevisions = decodeMobileJSON[[]map[string]any](t, revisionsJSON)
		if len(mobileRevisions) >= len(desktopRevisions) || time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(mobileRevisions) != len(desktopRevisions) {
		t.Fatalf("mobile revision count = %d, want %d (matching desktop)", len(mobileRevisions), len(desktopRevisions))
	}

	// --- 5. Simultaneous offline edits: both actors edit the same note
	// without syncing in between; after sync both must converge to
	// identical content containing both edits. ---
	concurrent, err := desktopActor.CreateNote("", "Concurrent edit parity")
	if err != nil {
		t.Fatalf("desktop CreateNote(concurrent): %v", err)
	}
	baseUpdate, baseFormat := yjsUpdateFromScratch(t, "Original shared content")
	if err := desktopActor.CommitNoteBody(CommitNoteBodyRequest{NoteID: concurrent.ID, UpdateBase64: baseUpdate, UpdateFormat: baseFormat}); err != nil {
		t.Fatalf("desktop CommitNoteBody(base): %v", err)
	}
	mobileConcurrentID := waitUntilMobileSeesNote(t, "Concurrent edit parity")
	mobileBaseJSON, err := mobileActor.GetNote("get-base", mobileConcurrentID)
	if err != nil {
		t.Fatalf("mobile GetNote(base): %v", err)
	}
	mobileBaseFields := decodeMobileJSON[map[string]any](t, mobileBaseJSON)
	mobileBaseRevision := mobileBaseFields["base_revision"].(string)

	// Neither side syncs between these two edits: each is made against a
	// snapshot the other side never saw, the real shape of two offline
	// devices editing independently.
	desktopEditUpdate, desktopEditFormat := yjsUpdateFromScratch(t, "Desktop concurrent addition")
	if err := desktopActor.CommitNoteBody(CommitNoteBodyRequest{NoteID: concurrent.ID, UpdateBase64: desktopEditUpdate, UpdateFormat: desktopEditFormat}); err != nil {
		t.Fatalf("desktop CommitNoteBody(concurrent): %v", err)
	}
	if _, err := mobileActor.SaveNote("save-concurrent", mobileConcurrentID, "Concurrent edit parity", "Original shared content\nMobile concurrent addition", mobileBaseRevision); err != nil {
		t.Fatalf("mobile SaveNote(concurrent): %v", err)
	}

	deadline = time.Now().Add(20 * time.Second)
	var desktopFinal, mobileFinal string
	for {
		syncBoth(t)
		desktopFinal = desktopMarkdown(t, desktopActor, concurrent.ID)
		mobileFinalJSON, err := mobileActor.GetNote("get-final", mobileConcurrentID)
		if err != nil {
			t.Fatalf("mobile GetNote(final): %v", err)
		}
		mobileFinal = decodeMobileJSON[map[string]any](t, mobileFinalJSON)["body"].(string)
		if strings.Contains(desktopFinal, "Mobile concurrent addition") && strings.Contains(mobileFinal, "Desktop concurrent addition") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("concurrent edits never converged: desktop=%q mobile=%q", desktopFinal, mobileFinal)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(desktopFinal, "Desktop concurrent addition") || !strings.Contains(mobileFinal, "Mobile concurrent addition") {
		t.Fatalf("a concurrent edit was lost: desktop=%q mobile=%q", desktopFinal, mobileFinal)
	}
	if desktopFinal != mobileFinal {
		t.Fatalf("desktop and mobile did not converge to identical content: desktop=%q mobile=%q", desktopFinal, mobileFinal)
	}

	// --- 6. Delete/move conflicts: one actor deletes while the other
	// concurrently moves the same note to a different notebook, with no
	// sync in between. Note.Deleted and Note.NotebookID are independent
	// LWW fields (core/model/note.go), so both changes are expected to
	// survive rather than one silently overwriting the other. ---
	destinationNotebook, err := desktopActor.CreateNotebook("", "Conflict destination")
	if err != nil {
		t.Fatalf("desktop CreateNotebook(destination): %v", err)
	}
	syncBoth(t)
	deadline = time.Now().Add(20 * time.Second)
	for {
		notebooksJSON, err := mobileActor.ListNotebooks("list-notebooks")
		if err != nil {
			t.Fatalf("mobile ListNotebooks: %v", err)
		}
		found := false
		for _, nb := range decodeMobileJSON[[]map[string]any](t, notebooksJSON) {
			if nb["id"] == destinationNotebook.ID {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mobile actor never observed the destination notebook")
		}
		syncBoth(t)
		time.Sleep(25 * time.Millisecond)
	}

	if err := desktopActor.DeleteNote(concurrent.ID); err != nil {
		t.Fatalf("desktop DeleteNote: %v", err)
	}
	if err := mobileActor.MoveNote("move-conflict", mobileConcurrentID, destinationNotebook.ID); err != nil {
		t.Fatalf("mobile MoveNote: %v", err)
	}
	syncBoth(t)
	deadline = time.Now().Add(20 * time.Second)
	var desktopConflictView NoteDTO
	for {
		desktopConflictView, err = desktopActor.GetNote(concurrent.ID)
		if err != nil {
			t.Fatalf("desktop GetNote(conflict): %v", err)
		}
		if desktopConflictView.Deleted && desktopConflictView.NotebookID == destinationNotebook.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delete/move conflict never converged on desktop: %+v", desktopConflictView)
		}
		syncBoth(t)
		time.Sleep(25 * time.Millisecond)
	}
	mobileConflictJSON, err := mobileActor.GetNote("get-conflict", mobileConcurrentID)
	if err != nil {
		t.Fatalf("mobile GetNote(conflict): %v", err)
	}
	mobileConflictNote := decodeMobileJSON[map[string]any](t, mobileConflictJSON)["note"].(map[string]any)
	if mobileConflictNote["deleted"] != true || mobileConflictNote["notebook_id"] != destinationNotebook.ID {
		t.Fatalf("mobile did not converge with desktop on the delete/move conflict: %+v (desktop: %+v)", mobileConflictNote, desktopConflictView)
	}

	// --- 7. Reconnect: both actors make independent local changes while
	// "disconnected" (simply not syncing), then reconnecting (syncing)
	// converges the note list on both sides. ---
	if _, err := desktopActor.CreateNote("", "Created while desktop was ahead"); err != nil {
		t.Fatalf("desktop CreateNote(reconnect): %v", err)
	}
	if _, err := mobileActor.CreateNote("create-reconnect", "", "Created while mobile was ahead"); err != nil {
		t.Fatalf("mobile CreateNote(reconnect): %v", err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		syncBoth(t)
		desktopNotes, err := desktopActor.ListNotes()
		if err != nil {
			t.Fatalf("desktop ListNotes(reconnect): %v", err)
		}
		mobileNotesJSON, err := mobileActor.ListNotes("list-reconnect")
		if err != nil {
			t.Fatalf("mobile ListNotes(reconnect): %v", err)
		}
		mobileNotes := decodeMobileJSON[[]map[string]any](t, mobileNotesJSON)
		desktopHasBoth := containsTitle(desktopNoteTitles(desktopNotes), "Created while desktop was ahead") &&
			containsTitle(desktopNoteTitles(desktopNotes), "Created while mobile was ahead")
		mobileHasBoth := containsTitle(mobileNoteTitles(mobileNotes), "Created while desktop was ahead") &&
			containsTitle(mobileNoteTitles(mobileNotes), "Created while mobile was ahead")
		if desktopHasBoth && mobileHasBoth {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconnect never converged: desktop=%d notes, mobile=%d notes", len(desktopNotes), len(mobileNotes))
		}
		time.Sleep(25 * time.Millisecond)
	}

	// --- 8. Workspace switch: mobile switching away from the shared
	// workspace must isolate subsequent notes from desktop (which only
	// holds the shared workspace), and switching back must restore full
	// visibility - identically to the same-platform switch behavior
	// task 8.2/TestServiceWorkspaceSharingAcrossTwoAccounts already prove
	// in isolation. ---
	joinerWorkspacesJSON, err := mobileActor.ListWorkspaces("list-joiner-workspaces")
	if err != nil {
		t.Fatalf("mobile ListWorkspaces: %v", err)
	}
	var joinerOwnWorkspaceID string
	for _, ws := range decodeMobileJSON[[]mobileWorkspaceSummary](t, joinerWorkspacesJSON) {
		if ws.WorkspaceID != sharedWorkspaceID {
			joinerOwnWorkspaceID = ws.WorkspaceID
		}
	}
	if joinerOwnWorkspaceID == "" {
		t.Fatal("mobile actor unexpectedly has no separate solo workspace to switch to")
	}
	if err := mobileActor.SetActiveWorkspace("switch-away", joinerOwnWorkspaceID); err != nil {
		t.Fatalf("mobile SetActiveWorkspace(own): %v", err)
	}
	if _, err := mobileActor.CreateNote("create-isolated", "", "Isolated to mobile's own workspace"); err != nil {
		t.Fatalf("mobile CreateNote(isolated): %v", err)
	}
	syncBoth(t)
	desktopNotesAfterSwitch, err := desktopActor.ListNotes()
	if err != nil {
		t.Fatalf("desktop ListNotes(after switch): %v", err)
	}
	if containsTitle(desktopNoteTitles(desktopNotesAfterSwitch), "Isolated to mobile's own workspace") {
		t.Fatal("desktop (only in the shared workspace) unexpectedly saw a note created in mobile's separate own workspace")
	}

	if err := mobileActor.SetActiveWorkspace("switch-back", sharedWorkspaceID); err != nil {
		t.Fatalf("mobile SetActiveWorkspace(shared): %v", err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		notesJSON, err := mobileActor.ListNotes("list-after-switch-back")
		if err != nil {
			t.Fatalf("mobile ListNotes(after switch back): %v", err)
		}
		if containsTitle(mobileNoteTitles(decodeMobileJSON[[]map[string]any](t, notesJSON)), "Created while desktop was ahead") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("switching back to the shared workspace never restored visibility of its notes")
		}
		syncBoth(t)
		time.Sleep(25 * time.Millisecond)
	}
}

func desktopNoteTitles(notes []NoteDTO) []string {
	titles := make([]string, len(notes))
	for i, n := range notes {
		titles[i] = n.Title
	}
	return titles
}

func mobileNoteTitles(notes []map[string]any) []string {
	titles := make([]string, len(notes))
	for i, n := range notes {
		titles[i], _ = n["title"].(string)
	}
	return titles
}

func containsTitle(titles []string, want string) bool {
	for _, title := range titles {
		if title == want {
			return true
		}
	}
	return false
}

package account

import (
	"context"
	"errors"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/sync"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

func createTestAccount(t *testing.T) *Account {
	t.Helper()
	created, err := Create(context.Background(), CreateOptions{
		DatabasePath: tempDBPath(t),
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      newFakeWrapper(),
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() { created.Lock() })
	return created
}

func defaultWorkspaceID(t *testing.T, a *Account) model.ID {
	t.Helper()
	for id := range a.workspaceKeys {
		return id
	}
	t.Fatal("account has no workspace")
	return model.ID{}
}

func encodedInsertUpdate(t *testing.T, text string) []byte {
	t.Helper()
	doc := yjsadapter.New()
	defer doc.Close()
	if err := doc.Insert(noteBodyRoot, 0, text, nil); err != nil {
		t.Fatalf("build test update: %v", err)
	}
	update, err := doc.EncodeStateAsUpdate(yjsadapter.FormatV2)
	if err != nil {
		t.Fatalf("encode test update: %v", err)
	}
	return update
}

func TestCommitNoteBodyPersistsSnapshotFTSRevisionAndOutbox(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	noteClock, err := created.tick()
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.CreateNote(ctx, created.db, workspaceID, model.Nil, "Untitled", noteClock)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	update := encodedInsertUpdate(t, "hello world")
	newTitle := "My note"
	if err := created.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID:  workspaceID,
		NoteID:       note.ID,
		Update:       update,
		UpdateFormat: yjsadapter.FormatV2,
		Title:        &newTitle,
	}); err != nil {
		t.Fatalf("CommitNoteBody: %v", err)
	}

	gotNote, err := store.GetNote(ctx, created.db, note.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if gotNote.Title.Value != newTitle {
		t.Fatalf("title = %q, want %q", gotNote.Title.Value, newTitle)
	}

	state, ok, err := store.LoadCRDTState(ctx, created.db, note.ID)
	if err != nil || !ok {
		t.Fatalf("LoadCRDTState: ok=%v err=%v", ok, err)
	}
	entry := created.workspaceKeys[workspaceID]
	plaintext, err := corecrypto.UnpackAndOpenObject(entry.Key, noteSnapshotMetadata(workspaceID, note.ID), state.Snapshot)
	if err != nil {
		t.Fatalf("open persisted snapshot: %v", err)
	}
	var restored *yjsadapter.Document
	if err := plaintext.Use(func(snapshot []byte) error {
		var restoreErr error
		restored, restoreErr = yjsadapter.Restore(noteSnapshotFormat, snapshot)
		return restoreErr
	}); err != nil {
		t.Fatalf("restore persisted snapshot: %v", err)
	}
	plaintext.Close()
	defer restored.Close()
	if text, err := restored.Text(noteBodyRoot); err != nil || text != "hello world" {
		t.Fatalf("restored text = %q, err = %v", text, err)
	}

	var ftsTitle, ftsBody string
	if err := created.db.QueryRowContext(ctx, `SELECT title, body FROM notes_fts WHERE note_id = ?`, note.ID.Bytes()).Scan(&ftsTitle, &ftsBody); err != nil {
		t.Fatalf("query notes_fts: %v", err)
	}
	if ftsTitle != newTitle || ftsBody != "hello world" {
		t.Fatalf("fts row = (%q, %q)", ftsTitle, ftsBody)
	}

	var revisionCount int
	if err := created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions WHERE note_id = ?`, note.ID.Bytes()).Scan(&revisionCount); err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	if revisionCount != 1 {
		t.Fatalf("revision count = %d, want 1", revisionCount)
	}

	var updateBytes, originDeviceID []byte
	if err := created.db.QueryRowContext(ctx, `SELECT update_bytes, origin_device_id FROM crdt_updates WHERE note_id = ?`, note.ID.Bytes()).Scan(&updateBytes, &originDeviceID); err != nil {
		t.Fatalf("query crdt_updates: %v", err)
	}
	if string(updateBytes) != string(update) {
		t.Fatal("crdt_updates row does not carry the applied update bytes")
	}
	gotOriginDevice, err := model.ParseID(originDeviceID)
	if err != nil || gotOriginDevice != created.DeviceID {
		t.Fatalf("crdt_updates origin device = %v, err = %v", gotOriginDevice, err)
	}

	var opID, keyID, nonce, ciphertext, signature []byte
	var physicalMS uint64
	var logical uint32
	row := created.db.QueryRowContext(ctx,
		`SELECT op_id, key_id, nonce, ciphertext, signature, physical_ms, logical FROM outbox WHERE workspace_id = ?`, workspaceID.Bytes())
	if err := row.Scan(&opID, &keyID, &nonce, &ciphertext, &signature, &physicalMS, &logical); err != nil {
		t.Fatalf("query outbox: %v", err)
	}

	sigInput, err := corecrypto.CanonicalOperationSignatureInput(corecrypto.OperationSignatureFields{
		OpID: opID, WorkspaceID: workspaceID.Bytes(), DeviceID: created.DeviceID.Bytes(),
		HLCPhysicalMS: physicalMS, HLCLogical: logical, HLCDeviceID: created.DeviceID.Bytes(),
		KeyID: keyID, Nonce: nonce, Ciphertext: ciphertext,
	})
	if err != nil {
		t.Fatalf("canonical signature input: %v", err)
	}
	if err := corecrypto.VerifyCanonical(corecrypto.CryptoProfileV1, created.DevicePublicKey, corecrypto.SignatureDomainOperation, sigInput, signature); err != nil {
		t.Fatalf("verify outbox signature: %v", err)
	}

	opMetadata := corecrypto.ObjectMetadata{
		SchemaVersion: corecrypto.SchemaVersionV1,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   workspaceID.Bytes(),
		ObjectID:      opID,
		ObjectType:    corecrypto.ObjectTypeOperationPayload,
		KeyID:         keyID,
	}
	opPlaintext, err := corecrypto.OpenObject(entry.Key, corecrypto.EncryptedObject{Metadata: opMetadata, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		t.Fatalf("open outbox payload: %v", err)
	}
	defer opPlaintext.Close()
	var decoded sync.NoteBodyOperation
	if err := opPlaintext.Use(func(data []byte) error {
		var decodeErr error
		decoded, decodeErr = sync.DecodeNoteBodyOperation(data)
		return decodeErr
	}); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	if decoded.NoteID != note.ID || decoded.Title == nil || *decoded.Title != newTitle {
		t.Fatalf("decoded operation = %+v", decoded)
	}
}

func TestCommitNoteBodySecondEditAccumulatesOnExistingSnapshot(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	noteClock, err := created.tick()
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.CreateNote(ctx, created.db, workspaceID, model.Nil, "Untitled", noteClock)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	firstDoc := yjsadapter.New()
	if err := firstDoc.Insert(noteBodyRoot, 0, "hello", nil); err != nil {
		t.Fatal(err)
	}
	firstUpdate, err := firstDoc.EncodeStateAsUpdate(yjsadapter.FormatV2)
	if err != nil {
		t.Fatal(err)
	}
	if err := created.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID: workspaceID, NoteID: note.ID, Update: firstUpdate, UpdateFormat: yjsadapter.FormatV2,
	}); err != nil {
		t.Fatalf("first CommitNoteBody: %v", err)
	}

	if err := firstDoc.Insert(noteBodyRoot, 5, " world", nil); err != nil {
		t.Fatal(err)
	}
	secondUpdate, err := firstDoc.EncodeStateAsUpdate(yjsadapter.FormatV2)
	firstDoc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := created.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID: workspaceID, NoteID: note.ID, Update: secondUpdate, UpdateFormat: yjsadapter.FormatV2,
	}); err != nil {
		t.Fatalf("second CommitNoteBody: %v", err)
	}

	state, ok, err := store.LoadCRDTState(ctx, created.db, note.ID)
	if err != nil || !ok {
		t.Fatalf("LoadCRDTState: ok=%v err=%v", ok, err)
	}
	entry := created.workspaceKeys[workspaceID]
	plaintext, err := corecrypto.UnpackAndOpenObject(entry.Key, noteSnapshotMetadata(workspaceID, note.ID), state.Snapshot)
	if err != nil {
		t.Fatalf("open persisted snapshot: %v", err)
	}
	defer plaintext.Close()
	var text string
	if err := plaintext.Use(func(snapshot []byte) error {
		doc, err := yjsadapter.Restore(noteSnapshotFormat, snapshot)
		if err != nil {
			return err
		}
		defer doc.Close()
		text, err = doc.Text(noteBodyRoot)
		return err
	}); err != nil {
		t.Fatalf("restore accumulated snapshot: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("accumulated text = %q, want %q", text, "hello world")
	}

	var revisionCount int
	if err := created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions WHERE note_id = ?`, note.ID.Bytes()).Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if revisionCount != 2 {
		t.Fatalf("revision count = %d, want 2", revisionCount)
	}
	var outboxCount int
	if err := created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE workspace_id = ?`, workspaceID.Bytes()).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 2 {
		t.Fatalf("outbox count = %d, want 2", outboxCount)
	}
}

func TestCommitNoteBodyRejectsMalformedUpdateWithoutPartialWrites(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	noteClock, err := created.tick()
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.CreateNote(ctx, created.db, workspaceID, model.Nil, "Untitled", noteClock)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	err = created.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID: workspaceID, NoteID: note.ID, Update: []byte{0xff, 0xff, 0xff}, UpdateFormat: yjsadapter.FormatV2,
	})
	if err == nil {
		t.Fatal("expected an error for a malformed update")
	}

	if _, ok, err := store.LoadCRDTState(ctx, created.db, note.ID); err != nil || ok {
		t.Fatalf("crdt_states should stay empty: ok=%v err=%v", ok, err)
	}
	var revisionCount, outboxCount, crdtUpdateCount int
	created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions WHERE note_id = ?`, note.ID.Bytes()).Scan(&revisionCount)
	created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE workspace_id = ?`, workspaceID.Bytes()).Scan(&outboxCount)
	created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM crdt_updates WHERE note_id = ?`, note.ID.Bytes()).Scan(&crdtUpdateCount)
	if revisionCount != 0 || outboxCount != 0 || crdtUpdateCount != 0 {
		t.Fatalf("rollback left revisionCount=%d outboxCount=%d crdtUpdateCount=%d, want 0, 0, 0", revisionCount, outboxCount, crdtUpdateCount)
	}
}

func TestCommitNoteBodyRejectsUnknownWorkspaceAndLockedAccount(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	noteClock, err := created.tick()
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.CreateNote(ctx, created.db, workspaceID, model.Nil, "Untitled", noteClock)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	update := encodedInsertUpdate(t, "hi")

	unknownWorkspace, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := created.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID: unknownWorkspace, NoteID: note.ID, Update: update, UpdateFormat: yjsadapter.FormatV2,
	}); err == nil {
		t.Fatal("expected an error for an unknown workspace")
	}

	if err := created.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := created.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID: workspaceID, NoteID: note.ID, Update: update, UpdateFormat: yjsadapter.FormatV2,
	}); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("locked CommitNoteBody error = %v, want ErrAccountLocked", err)
	}
}

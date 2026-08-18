package account

import (
	"context"
	"fmt"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/sync"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

// noteBodyRoot is the fixed Y.Text root name for a note's rich-text body.
const noteBodyRoot = "body"

// noteSnapshotFormat is the canonical Yjs update encoding Beresta itself
// uses for everything it stores or signs: the CRDT state persisted in
// crdt_states, the delta bytes in each revision, and (via NoteBodyCommand)
// the operation an editor's own update format contributes to the outbox. V2
// is chosen for its more compact column-oriented encoding; nothing outside
// Beresta's own Go core decodes these bytes, so cross-format interoperability
// with other Yjs implementations is not a constraint on this choice.
const noteSnapshotFormat = yjsadapter.FormatV2

// NoteBodyCommand is one atomic local edit to a note's rich-text body,
// optionally paired with a title change made in the same editor save (for
// example, a debounced commit that includes both the body and a title the
// user just typed).
type NoteBodyCommand struct {
	WorkspaceID model.ID
	NoteID      model.ID
	// Update is a Yjs update to apply to the note's body, in UpdateFormat.
	Update       []byte
	UpdateFormat yjsadapter.Format
	// Title, when non-nil, also renames the note in the same transaction.
	Title *string
}

// CommitNoteBody atomically applies a CRDT update (and optional title
// change) to one note and persists every derived artifact needed to keep the
// local store consistent: the new CRDT state and state vector, the FTS5
// search index, an encrypted delta revision, and a signed encrypted outbox
// operation recording the change for a future synchronization transport.
func (a *Account) CommitNoteBody(ctx context.Context, cmd NoteBodyCommand) error {
	if len(cmd.Update) == 0 {
		return fmt.Errorf("account: note body command requires an update")
	}

	db, entry, deviceID, devicePrivate, err := a.workspaceSession(cmd.WorkspaceID)
	if err != nil {
		return err
	}

	clock, err := a.tick()
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin note body transaction: %w", err)
	}
	defer tx.Rollback()

	note, err := store.GetNote(ctx, tx, cmd.NoteID)
	if err != nil {
		return err
	}
	if note.WorkspaceID != cmd.WorkspaceID {
		return store.ErrWrongWorkspace
	}

	doc, err := loadNoteDocument(ctx, tx, entry, cmd.WorkspaceID, cmd.NoteID)
	if err != nil {
		return err
	}
	defer doc.Close()

	if err := doc.ApplyUpdate(cmd.UpdateFormat, cmd.Update); err != nil {
		return fmt.Errorf("account: apply note body update: %w", err)
	}

	snapshot, err := doc.EncodeStateAsUpdate(noteSnapshotFormat)
	if err != nil {
		return fmt.Errorf("account: encode note snapshot: %w", err)
	}
	stateVector, err := doc.EncodeStateVector()
	if err != nil {
		return fmt.Errorf("account: encode note state vector: %w", err)
	}
	markdown, err := doc.Markdown(noteBodyRoot)
	if err != nil {
		return fmt.Errorf("account: project note markdown: %w", err)
	}

	if err := saveNoteSnapshot(ctx, tx, entry, cmd.WorkspaceID, cmd.NoteID, snapshot, stateVector, clock.PhysicalMS); err != nil {
		return err
	}

	title := note.Title.Value
	if cmd.Title != nil {
		title = *cmd.Title
		if err := store.SetNoteTitle(ctx, tx, cmd.NoteID, title, clock); err != nil {
			return err
		}
	}

	if err := store.ReplaceNoteFTS(ctx, tx, cmd.NoteID, title, markdown); err != nil {
		return err
	}

	if err := store.InsertCRDTUpdate(ctx, tx, cmd.NoteID, cmd.Update, deviceID, clock.PhysicalMS); err != nil {
		return err
	}

	revisionID, err := model.NewID()
	if err != nil {
		return err
	}
	if err := saveNoteRevision(ctx, tx, entry, cmd.WorkspaceID, cmd.NoteID, revisionID, cmd.Update, clock.PhysicalMS); err != nil {
		return err
	}

	if err := writeNoteOutboxOperation(ctx, tx, entry, devicePrivate, cmd, deviceID, clock); err != nil {
		return err
	}

	if err := store.AdvanceDeviceClock(ctx, tx, deviceID, clock); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("account: commit note body transaction: %w", err)
	}
	return nil
}

// loadNoteDocument restores a note's CRDT document from its persisted
// snapshot, or returns a fresh empty document for a note that has never had
// a body command applied.
func loadNoteDocument(ctx context.Context, exec store.Executor, entry workspaceKeyEntry, workspaceID, noteID model.ID) (*yjsadapter.Document, error) {
	state, ok, err := store.LoadCRDTState(ctx, exec, noteID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return yjsadapter.New(), nil
	}

	metadata := noteSnapshotMetadata(workspaceID, noteID)
	plaintext, err := corecrypto.UnpackAndOpenObject(entry.Key, metadata, state.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("account: open note snapshot: %w", err)
	}
	defer plaintext.Close()

	var doc *yjsadapter.Document
	var restoreErr error
	err = plaintext.Use(func(snapshot []byte) error {
		doc, restoreErr = yjsadapter.Restore(noteSnapshotFormat, snapshot)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if restoreErr != nil {
		return nil, fmt.Errorf("account: restore note snapshot: %w", restoreErr)
	}
	return doc, nil
}

func saveNoteSnapshot(ctx context.Context, exec store.Executor, entry workspaceKeyEntry, workspaceID, noteID model.ID, snapshot, stateVector []byte, updatedUnixMS uint64) error {
	plaintext, err := corecrypto.TakeSecret(append([]byte(nil), snapshot...))
	if err != nil {
		return err
	}
	defer plaintext.Close()

	metadata := noteSnapshotMetadata(workspaceID, noteID)
	metadata.KeyID = entry.KeyID
	blob, err := corecrypto.EncryptAndPackObject(entry.Key, metadata, plaintext)
	if err != nil {
		return fmt.Errorf("account: encrypt note snapshot: %w", err)
	}
	return store.UpsertCRDTState(ctx, exec, noteID, store.CRDTState{Snapshot: blob, StateVector: stateVector}, updatedUnixMS)
}

func saveNoteRevision(ctx context.Context, exec store.Executor, entry workspaceKeyEntry, workspaceID, noteID, revisionID model.ID, delta []byte, createdUnixMS uint64) error {
	plaintext, err := corecrypto.TakeSecret(append([]byte(nil), delta...))
	if err != nil {
		return err
	}
	defer plaintext.Close()

	metadata := corecrypto.ObjectMetadata{
		SchemaVersion: corecrypto.SchemaVersionV1,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   workspaceID.Bytes(),
		ObjectID:      noteID.Bytes(),
		ObjectType:    corecrypto.ObjectTypeRevision,
		KeyID:         entry.KeyID,
	}
	blob, err := corecrypto.EncryptAndPackObject(entry.Key, metadata, plaintext)
	if err != nil {
		return fmt.Errorf("account: encrypt note revision: %w", err)
	}
	return store.InsertRevision(ctx, exec, revisionID, noteID, store.RevisionKindDelta, blob, createdUnixMS)
}

func writeNoteOutboxOperation(ctx context.Context, exec store.Executor, entry workspaceKeyEntry, devicePrivate *corecrypto.Secret, cmd NoteBodyCommand, deviceID model.ID, clock model.HLC) error {
	payload, err := sync.EncodeNoteBodyOperation(sync.NoteBodyOperation{
		NoteID:     cmd.NoteID,
		CRDTUpdate: cmd.Update,
		Title:      cmd.Title,
	})
	if err != nil {
		return fmt.Errorf("account: encode note body operation: %w", err)
	}
	return writeOutboxOperation(ctx, exec, entry, devicePrivate, cmd.WorkspaceID, deviceID, clock, payload)
}

// writeOutboxOperation encrypts, signs, and appends one plaintext
// operation-payload to the outbox for a future synchronization transport.
// Every local note/notebook/tag/attachment mutation service funnels through
// this one place so the encrypt/sign/insert steps stay identical regardless
// of which payload encoder produced the plaintext.
func writeOutboxOperation(ctx context.Context, exec store.Executor, entry workspaceKeyEntry, devicePrivate *corecrypto.Secret, workspaceID, deviceID model.ID, clock model.HLC, payload []byte) error {
	opID, err := model.NewID()
	if err != nil {
		return err
	}

	plaintext, err := corecrypto.TakeSecret(payload)
	if err != nil {
		return err
	}
	defer plaintext.Close()

	metadata := corecrypto.ObjectMetadata{
		SchemaVersion: corecrypto.SchemaVersionV1,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   workspaceID.Bytes(),
		ObjectID:      opID.Bytes(),
		ObjectType:    corecrypto.ObjectTypeOperationPayload,
		KeyID:         entry.KeyID,
	}
	encrypted, err := corecrypto.EncryptObject(entry.Key, metadata, plaintext)
	if err != nil {
		return fmt.Errorf("account: encrypt outbox operation: %w", err)
	}

	signatureInput, err := corecrypto.CanonicalOperationSignatureInput(corecrypto.OperationSignatureFields{
		OpID:          opID.Bytes(),
		WorkspaceID:   workspaceID.Bytes(),
		DeviceID:      deviceID.Bytes(),
		HLCPhysicalMS: clock.PhysicalMS,
		HLCLogical:    clock.Logical,
		HLCDeviceID:   clock.DeviceID.Bytes(),
		KeyID:         entry.KeyID,
		Nonce:         encrypted.Nonce,
		Ciphertext:    encrypted.Ciphertext,
	})
	if err != nil {
		return fmt.Errorf("account: build operation signature input: %w", err)
	}
	signature, err := corecrypto.SignCanonical(corecrypto.CryptoProfileV1, devicePrivate, corecrypto.SignatureDomainOperation, signatureInput)
	if err != nil {
		return fmt.Errorf("account: sign outbox operation: %w", err)
	}

	return store.InsertOutboxOperation(ctx, exec, store.OutboxOperation{
		OpID:        opID,
		WorkspaceID: workspaceID,
		DeviceID:    deviceID,
		Clock:       clock,
		KeyID:       entry.KeyID,
		Nonce:       encrypted.Nonce,
		Ciphertext:  encrypted.Ciphertext,
		Signature:   signature,
	}, clock.PhysicalMS)
}

func noteSnapshotMetadata(workspaceID, noteID model.ID) corecrypto.ObjectMetadata {
	return corecrypto.ObjectMetadata{
		SchemaVersion: corecrypto.SchemaVersionV1,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   workspaceID.Bytes(),
		ObjectID:      noteID.Bytes(),
		ObjectType:    corecrypto.ObjectTypeNoteSnapshot,
	}
}

package account

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	coresync "github.com/beresta-app/beresta/core/sync"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

type SyncProcessorOptions struct {
	MaxFutureSkew time.Duration
	Now           func() time.Time
}

// SyncProcessor authenticates and applies remote operations for an unlocked
// account. It keeps decryption and CRDT/LWW merging in the shared Go core.
type SyncProcessor struct {
	account *Account
	options SyncProcessorOptions
}

func NewSyncProcessor(account *Account, options SyncProcessorOptions) (*SyncProcessor, error) {
	if account == nil {
		return nil, errors.New("account: sync processor requires an account")
	}
	if options.MaxFutureSkew <= 0 {
		options.MaxFutureSkew = model.DefaultMaxFutureSkew
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &SyncProcessor{account: account, options: options}, nil
}

func (p *SyncProcessor) Verify(ctx context.Context, operation coresync.WireOperation) (coresync.VerifiedOperation, error) {
	db, entry, _, _, err := p.account.workspaceSession(operation.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if operation.Clock.PhysicalMS > uint64(p.options.Now().Add(p.options.MaxFutureSkew).UnixMilli()) {
		return nil, errors.New("operation HLC exceeds future-skew limit")
	}
	if !equalKeyID(entry, operation.KeyID) {
		// The operation was not encrypted under the current workspace key -
		// it may predate a rotation. Historical-key reads let this device
		// still verify and decrypt it with a retained older key instead of
		// rejecting valid history (see docs/crypto-spec.md key rotation).
		historical, ok := p.account.workspaceKeyByID(operation.WorkspaceID, operation.KeyID)
		if !ok {
			return nil, errors.New("operation uses an unavailable workspace key")
		}
		entry = historical
	}
	var publicKey []byte
	var status int
	if err := db.QueryRowContext(ctx, `SELECT public_key, status FROM devices WHERE id = ?`, operation.DeviceID.Bytes()).Scan(&publicKey, &status); err != nil {
		return nil, fmt.Errorf("load operation device: %w", err)
	}
	// Revocation prevents the server from sequencing new operations. History
	// accepted before that boundary must remain verifiable during late pull or
	// snapshot replay, so retain and accept the authenticated historical key.
	if (status != 1 && status != 2) || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("operation device key is unavailable")
	}
	input, err := coresync.OperationSignatureInput(operation)
	if err != nil {
		return nil, err
	}
	if err := corecrypto.VerifyCanonical(corecrypto.CryptoProfileV1, publicKey, corecrypto.SignatureDomainOperation, input, operation.Signature); err != nil {
		return nil, errors.New("operation signature verification failed")
	}
	metadata := corecrypto.ObjectMetadata{
		SchemaVersion: corecrypto.SchemaVersionV1,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   operation.WorkspaceID.Bytes(),
		ObjectID:      operation.OpID.Bytes(),
		ObjectType:    corecrypto.ObjectTypeOperationPayload,
		KeyID:         operation.KeyID,
	}
	plaintext, err := corecrypto.OpenObject(entry.Key, corecrypto.EncryptedObject{Metadata: metadata, Nonce: operation.Nonce, Ciphertext: operation.Ciphertext})
	if err != nil {
		return nil, errors.New("operation ciphertext authentication failed")
	}
	defer plaintext.Close()
	var payload []byte
	if err := plaintext.Use(func(value []byte) error {
		payload = append([]byte(nil), value...)
		return nil
	}); err != nil {
		return nil, err
	}
	if body, bodyErr := coresync.DecodeNoteBodyOperation(payload); bodyErr == nil {
		return &verifiedNoteOperation{account: p.account, entry: entry, wire: operation, body: &body}, nil
	}
	metadataOp, metadataErr := coresync.DecodeNoteMetadataOperation(payload)
	if metadataErr != nil {
		return nil, errors.New("unknown or malformed encrypted operation payload")
	}
	return &verifiedNoteOperation{account: p.account, entry: entry, wire: operation, metadata: &metadataOp}, nil
}

func equalKeyID(entry workspaceKeyEntry, candidate []byte) bool {
	if len(entry.KeyID) != len(candidate) {
		return false
	}
	var difference byte
	for i := range candidate {
		difference |= entry.KeyID[i] ^ candidate[i]
	}
	return difference == 0
}

type verifiedNoteOperation struct {
	account  *Account
	entry    workspaceKeyEntry
	wire     coresync.WireOperation
	body     *coresync.NoteBodyOperation
	metadata *coresync.NoteMetadataOperation
}

func (v *verifiedNoteOperation) Apply(ctx context.Context, syncTx coresync.SyncTx) error {
	tx, ok := syncTx.(store.Executor)
	if !ok {
		return errors.New("account: sync transaction lacks repository operations")
	}
	noteID := v.metadataNoteID()
	note, err := store.GetNote(ctx, tx, noteID)
	if errors.Is(err, store.ErrNotFound) && v.body != nil {
		title := ""
		if v.body.Title != nil {
			title = *v.body.Title
		}
		note, err = store.InsertReplicatedNote(ctx, tx, noteID, v.wire.WorkspaceID, title, v.wire.Clock)
		if err == nil {
			err = store.ReplaceNoteFTS(ctx, tx, noteID, title, "")
		}
	}
	if err != nil {
		return err
	}
	if note.WorkspaceID != v.wire.WorkspaceID {
		return store.ErrWrongWorkspace
	}
	if v.body != nil {
		if !note.Deleted.Value || v.wire.Clock.Compare(note.Deleted.Clock) > 0 {
			if err := v.applyBody(ctx, tx, note); err != nil {
				return err
			}
		}
	} else if err := v.applyMetadata(ctx, tx); err != nil {
		return err
	}
	merged, err := v.account.clock.Observe(v.wire.Clock)
	if err != nil {
		return err
	}
	return store.AdvanceDeviceClock(ctx, tx, v.account.DeviceID, merged)
}

func (v *verifiedNoteOperation) metadataNoteID() model.ID {
	if v.body != nil {
		return v.body.NoteID
	}
	return v.metadata.NoteID
}

func (v *verifiedNoteOperation) applyBody(ctx context.Context, tx store.Executor, note model.Note) error {
	doc, err := loadNoteDocument(ctx, tx, v.account, v.wire.WorkspaceID, v.body.NoteID)
	if err != nil {
		return err
	}
	defer func() { doc.Close() }()
	if len(v.body.CRDTUpdate) != 0 {
		doc, err = applyUnknownYjsFormat(doc, v.body.CRDTUpdate)
		if err != nil {
			return err
		}
		snapshot, err := doc.EncodeStateAsUpdate(noteSnapshotFormat)
		if err != nil {
			return err
		}
		vector, err := doc.EncodeStateVector()
		if err != nil {
			return err
		}
		markdown, err := doc.Markdown(noteBodyRoot)
		if err != nil {
			return err
		}
		if err := saveNoteSnapshot(ctx, tx, v.entry, v.wire.WorkspaceID, v.body.NoteID, snapshot, vector, v.wire.Clock.PhysicalMS); err != nil {
			return err
		}
		if err := store.InsertCRDTUpdate(ctx, tx, v.body.NoteID, v.body.CRDTUpdate, v.wire.DeviceID, v.wire.Clock.PhysicalMS); err != nil {
			return err
		}
		title := note.Title.Value
		if v.body.Title != nil && v.wire.Clock.Compare(note.Title.Clock) > 0 {
			title = *v.body.Title
		}
		if err := store.ReplaceNoteFTS(ctx, tx, v.body.NoteID, title, markdown); err != nil {
			return err
		}
	}
	if v.body.Title != nil {
		return store.SetNoteTitle(ctx, tx, v.body.NoteID, *v.body.Title, v.wire.Clock)
	}
	return nil
}

func applyUnknownYjsFormat(doc *yjsadapter.Document, update []byte) (*yjsadapter.Document, error) {
	snapshot, err := doc.EncodeStateAsUpdate(yjsadapter.FormatV2)
	if err != nil {
		return doc, err
	}
	if err := doc.ApplyUpdate(yjsadapter.FormatV2, update); err == nil {
		return doc, nil
	}
	// A decoder failure must never leave a partially mutated document as the
	// input to the fallback decoder. Restore the pre-apply state first.
	doc.Close()
	doc, err = yjsadapter.Restore(yjsadapter.FormatV2, snapshot)
	if err != nil {
		return doc, fmt.Errorf("account: restore Yjs state after decoder failure: %w", err)
	}
	if err := doc.ApplyUpdate(yjsadapter.FormatV1, update); err != nil {
		return doc, fmt.Errorf("account: reject malformed Yjs update: %w", err)
	}
	return doc, nil
}

func (v *verifiedNoteOperation) applyMetadata(ctx context.Context, tx store.Executor) error {
	op := v.metadata
	switch op.Kind {
	case coresync.NoteMetadataKindNotebook:
		// A newly connected device can receive a "move note to notebook X"
		// operation before notebook X's own creation operation. Filing a note
		// back at the workspace root (NotebookID zero) never needs a
		// notebooks row, but any other target must exist first to satisfy
		// notes.notebook_id's foreign key.
		if !op.NotebookID.IsZero() {
			if err := store.EnsureNotebookPlaceholder(ctx, tx, v.wire.WorkspaceID, op.NotebookID, v.wire.Clock); err != nil {
				return err
			}
		}
		return store.SetNoteNotebook(ctx, tx, op.NoteID, op.NotebookID, v.wire.Clock)
	case coresync.NoteMetadataKindTag:
		// Tag definitions are structural workspace state and may not yet be
		// materialized on a newly connected client. Preserve the membership
		// operation with a hidden placeholder instead of quarantining the
		// whole workspace on note_tags' foreign key.
		if err := store.EnsureTagPlaceholder(ctx, tx, v.wire.WorkspaceID, op.TagID, v.wire.Clock); err != nil {
			return err
		}
		return store.SetNoteTag(ctx, tx, op.NoteID, op.TagID, op.TagPresent, v.wire.Clock)
	case coresync.NoteMetadataKindFlags:
		return store.SetNoteFlags(ctx, tx, op.NoteID, op.Flags, v.wire.Clock)
	case coresync.NoteMetadataKindDeleted:
		return store.SetNoteDeleted(ctx, tx, op.NoteID, op.Deleted, v.wire.Clock)
	case coresync.NoteMetadataKindAttachment:
		blobID, err := store.ParseBlobID(op.AttachmentBlobID)
		if err != nil {
			return err
		}
		// Attachment-reference operations carry only the content-addressed
		// blob ID. A newly connected device can therefore receive the note
		// operation before the attachment catalog is available locally. Keep
		// a placeholder row to satisfy the relationship's foreign key and,
		// crucially, allow the rest of the workspace history to be applied.
		if _, err := store.EnsureAttachmentPlaceholder(ctx, tx, v.wire.WorkspaceID, blobID, int64(v.wire.Clock.PhysicalMS)); err != nil {
			return err
		}
		return store.SetNoteAttachment(ctx, tx, op.NoteID, blobID, op.AttachmentPresent, v.wire.Clock, int64(v.wire.Clock.PhysicalMS))
	default:
		return errors.New("account: unsupported note metadata mutation")
	}
}

var _ coresync.OperationProcessor = (*SyncProcessor)(nil)
var _ coresync.VerifiedOperation = (*verifiedNoteOperation)(nil)

// Keep the database/sql dependency explicit: the shared transaction contract
// is intentionally satisfied by *sql.Tx, never by a cross-thread callback.
var _ coresync.SyncTx = (*sql.Tx)(nil)

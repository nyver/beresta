package account

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	coresync "github.com/beresta-app/beresta/core/sync"
)

var snapshotArchiveMagic = [4]byte{'B', 'S', 'N', '1'}

// CreateWorkspaceSnapshot publishes a complete authenticated replay archive
// of the operation history currently known at a contiguous server cursor.
// The server sees only one workspace-key ciphertext and cannot inspect notes.
func (a *Account) CreateWorkspaceSnapshot(ctx context.Context, workspaceID model.ID, repository *store.SyncRepository) (coresync.Snapshot, error) {
	db, entry, deviceID, devicePrivate, err := a.workspaceSession(workspaceID)
	if err != nil {
		return coresync.Snapshot{}, err
	}
	cursor, err := repository.Cursor(ctx, workspaceID)
	if err != nil || cursor.LastSequence == 0 {
		return coresync.Snapshot{}, errors.New("account: a non-empty contiguous cursor is required for a snapshot")
	}
	operations, err := snapshotOperations(ctx, db, workspaceID, cursor.LastSequence)
	if err != nil {
		return coresync.Snapshot{}, err
	}
	payload, err := encodeSnapshotOperations(operations)
	if err != nil {
		return coresync.Snapshot{}, err
	}
	plaintext, err := corecrypto.TakeSecret(payload)
	if err != nil {
		return coresync.Snapshot{}, err
	}
	defer plaintext.Close()
	id, err := model.NewID()
	if err != nil {
		return coresync.Snapshot{}, err
	}
	clock, err := a.tick()
	if err != nil {
		return coresync.Snapshot{}, err
	}
	encrypted, err := corecrypto.EncryptObject(entry.Key, corecrypto.ObjectMetadata{
		SchemaVersion: corecrypto.SchemaVersionV1, CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID: workspaceID.Bytes(), ObjectID: id.Bytes(), ObjectType: corecrypto.ObjectTypeWorkspaceSnapshot, KeyID: entry.KeyID,
	}, plaintext)
	if err != nil {
		return coresync.Snapshot{}, err
	}
	digest := sha256.Sum256(encrypted.Ciphertext)
	snapshot := coresync.Snapshot{ID: id, WorkspaceID: workspaceID, BaseSequence: cursor.LastSequence, CursorEpoch: cursor.Epoch,
		KeyID: append([]byte(nil), entry.KeyID...), CreatorDeviceID: deviceID, Clock: clock, Nonce: encrypted.Nonce,
		CiphertextHash: digest[:], Ciphertext: encrypted.Ciphertext}
	if err := coresync.SignSnapshot(&snapshot, devicePrivate); err != nil {
		return coresync.Snapshot{}, err
	}
	// This device authored and is about to publish snapshot, so it already
	// implicitly acknowledges it; recording that now lets local operation-log
	// garbage collection (see oplog_gc.go) use it as a safe pruning boundary
	// without waiting on a round trip through the server.
	if err := saveSnapshotCatalogRow(ctx, db, snapshot, clock.PhysicalMS, true); err != nil {
		return coresync.Snapshot{}, err
	}
	return snapshot, nil
}

// saveSnapshotCatalogRow records one snapshot this device has authenticated
// (either just created, or just replayed from a peer) in the local
// snapshot catalog, so RunOperationLogGarbageCollection has a durable,
// queryable record of how far this device's local history is safely
// covered. It does not store plaintext: only the same opaque fields a
// server would see.
func saveSnapshotCatalogRow(ctx context.Context, db store.Executor, snapshot coresync.Snapshot, nowUnixMS uint64, acknowledged bool) error {
	var acknowledgedAt any
	if acknowledged {
		acknowledgedAt = nowUnixMS
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO snapshots (id, workspace_id, key_id, base_seq, ciphertext_hash, signature, created_unix_ms, acknowledged_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET acknowledged_unix_ms = COALESCE(snapshots.acknowledged_unix_ms, excluded.acknowledged_unix_ms)`,
		snapshot.ID.Bytes(), snapshot.WorkspaceID.Bytes(), snapshot.KeyID, snapshot.BaseSequence,
		snapshot.CiphertextHash, snapshot.Signature, nowUnixMS, acknowledgedAt)
	if err != nil {
		return fmt.Errorf("account: record snapshot catalog row: %w", err)
	}
	return nil
}

// ApplyWorkspaceSnapshot authenticates, decrypts, and transactionally replays
// only the portion newer than the local cursor, then returns an acknowledgement
// signed by this device for server compaction accounting.
func (a *Account) ApplyWorkspaceSnapshot(ctx context.Context, snapshot coresync.Snapshot, repository *store.SyncRepository, processor *SyncProcessor) (coresync.SnapshotAcknowledgement, error) {
	db, entry, deviceID, devicePrivate, err := a.workspaceSession(snapshot.WorkspaceID)
	if err != nil {
		return coresync.SnapshotAcknowledgement{}, err
	}
	if !equalKeyID(entry, snapshot.KeyID) {
		historical, ok := a.workspaceKeyByID(snapshot.WorkspaceID, snapshot.KeyID)
		if !ok {
			return coresync.SnapshotAcknowledgement{}, errors.New("account: snapshot key is unavailable")
		}
		entry = historical
	}
	var creatorPublic []byte
	var status int
	if err := db.QueryRowContext(ctx, `SELECT public_key, status FROM devices WHERE id = ?`, snapshot.CreatorDeviceID.Bytes()).Scan(&creatorPublic, &status); err != nil {
		return coresync.SnapshotAcknowledgement{}, err
	}
	if (status != 1 && status != 2) || len(creatorPublic) != ed25519.PublicKeySize || coresync.VerifySnapshot(snapshot, creatorPublic) != nil {
		return coresync.SnapshotAcknowledgement{}, errors.New("account: snapshot signature verification failed")
	}
	plaintext, err := corecrypto.OpenObject(entry.Key, corecrypto.EncryptedObject{Metadata: corecrypto.ObjectMetadata{
		SchemaVersion: corecrypto.SchemaVersionV1, CryptoProfile: corecrypto.CryptoProfileV1, WorkspaceID: snapshot.WorkspaceID.Bytes(),
		ObjectID: snapshot.ID.Bytes(), ObjectType: corecrypto.ObjectTypeWorkspaceSnapshot, KeyID: snapshot.KeyID,
	}, Nonce: snapshot.Nonce, Ciphertext: snapshot.Ciphertext})
	if err != nil {
		return coresync.SnapshotAcknowledgement{}, errors.New("account: snapshot ciphertext authentication failed")
	}
	defer plaintext.Close()
	var payload []byte
	if err := plaintext.Use(func(value []byte) error { payload = append([]byte(nil), value...); return nil }); err != nil {
		return coresync.SnapshotAcknowledgement{}, err
	}
	defer clear(payload)
	operations, err := decodeSnapshotOperations(payload)
	if err != nil || len(operations) == 0 || operations[0].Sequence != 1 || uint64(len(operations)) != snapshot.BaseSequence || operations[len(operations)-1].Sequence != snapshot.BaseSequence {
		return coresync.SnapshotAcknowledgement{}, errors.New("account: snapshot operation archive is incomplete")
	}
	cursor, err := repository.Cursor(ctx, snapshot.WorkspaceID)
	if err != nil {
		return coresync.SnapshotAcknowledgement{}, err
	}
	if cursor.Epoch != snapshot.CursorEpoch {
		return coresync.SnapshotAcknowledgement{}, errors.New("account: snapshot cursor epoch mismatch")
	}
	if cursor.LastSequence < snapshot.BaseSequence {
		start := sort.Search(len(operations), func(i int) bool { return operations[i].Sequence > cursor.LastSequence })
		if err := repository.ApplyPage(ctx, coresync.Cursor{WorkspaceID: snapshot.WorkspaceID, LastSequence: snapshot.BaseSequence, Epoch: snapshot.CursorEpoch}, operations[start:], processor, time.Now()); err != nil {
			return coresync.SnapshotAcknowledgement{}, err
		}
	}
	ack := coresync.SnapshotAcknowledgement{SnapshotID: snapshot.ID, WorkspaceID: snapshot.WorkspaceID, DeviceID: deviceID,
		BaseSequence: snapshot.BaseSequence, CiphertextHash: append([]byte(nil), snapshot.CiphertextHash...)}
	if err := coresync.SignSnapshotAcknowledgement(&ack, devicePrivate); err != nil {
		return coresync.SnapshotAcknowledgement{}, err
	}
	if err := saveSnapshotCatalogRow(ctx, db, snapshot, uint64(time.Now().UnixMilli()), true); err != nil {
		return coresync.SnapshotAcknowledgement{}, err
	}
	return ack, nil
}

func snapshotOperations(ctx context.Context, db *sql.DB, workspaceID model.ID, base uint64) ([]coresync.WireOperation, error) {
	if base > 1_000_000 {
		return nil, errors.New("account: snapshot operation count exceeds client limit")
	}
	queries := []string{
		`SELECT op_id, workspace_id, device_id, physical_ms, logical, key_id, nonce, ciphertext, signature, server_seq FROM inbox WHERE workspace_id = ? AND status = 2 AND server_seq <= ?`,
		`SELECT op_id, workspace_id, device_id, physical_ms, logical, key_id, nonce, ciphertext, signature, server_seq FROM outbox WHERE workspace_id = ? AND server_seq IS NOT NULL AND server_seq <= ?`,
	}
	bySequence := make(map[uint64]coresync.WireOperation, int(base))
	for _, query := range queries {
		rows, err := db.QueryContext(ctx, query, workspaceID.Bytes(), base)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var op coresync.WireOperation
			var opID, wsID, deviceID []byte
			if err := rows.Scan(&opID, &wsID, &deviceID, &op.Clock.PhysicalMS, &op.Clock.Logical, &op.KeyID, &op.Nonce, &op.Ciphertext, &op.Signature, &op.Sequence); err != nil {
				rows.Close()
				return nil, err
			}
			op.OpID, err = model.ParseID(opID)
			if err == nil {
				op.WorkspaceID, err = model.ParseID(wsID)
			}
			if err == nil {
				op.DeviceID, err = model.ParseID(deviceID)
			}
			if err != nil {
				rows.Close()
				return nil, err
			}
			op.Clock.DeviceID = op.DeviceID
			if previous, exists := bySequence[op.Sequence]; exists && previous.OpID != op.OpID {
				rows.Close()
				return nil, errors.New("account: conflicting snapshot sequence")
			}
			bySequence[op.Sequence] = op
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	operations := make([]coresync.WireOperation, 0, len(bySequence))
	for sequence := uint64(1); sequence <= base; sequence++ {
		op, exists := bySequence[sequence]
		if !exists {
			return nil, fmt.Errorf("account: snapshot history has a gap at sequence %d", sequence)
		}
		operations = append(operations, op)
	}
	return operations, nil
}

func encodeSnapshotOperations(operations []coresync.WireOperation) ([]byte, error) {
	result := append([]byte(nil), snapshotArchiveMagic[:]...)
	result = binary.BigEndian.AppendUint32(result, uint32(len(operations)))
	for _, operation := range operations {
		encoded, err := coresync.EncodeOperation(operation)
		if err != nil {
			return nil, err
		}
		result = binary.BigEndian.AppendUint32(result, uint32(len(encoded)))
		result = append(result, encoded...)
	}
	return result, nil
}

func decodeSnapshotOperations(payload []byte) ([]coresync.WireOperation, error) {
	if len(payload) < 8 || !bytes.Equal(payload[:4], snapshotArchiveMagic[:]) {
		return nil, errors.New("account: malformed snapshot archive")
	}
	count := binary.BigEndian.Uint32(payload[4:8])
	if count == 0 || count > 1_000_000 {
		return nil, errors.New("account: invalid snapshot operation count")
	}
	offset := 8
	result := make([]coresync.WireOperation, 0, count)
	for range count {
		if offset+4 > len(payload) {
			return nil, errors.New("account: truncated snapshot archive")
		}
		length := int(binary.BigEndian.Uint32(payload[offset : offset+4]))
		offset += 4
		if length <= 0 || offset+length > len(payload) {
			return nil, errors.New("account: invalid snapshot operation length")
		}
		op, err := coresync.DecodeOperation(payload[offset:offset+length], coresync.CodecLimits{})
		if err != nil {
			return nil, err
		}
		if len(result) != 0 && op.Sequence != result[len(result)-1].Sequence+1 {
			return nil, errors.New("account: non-contiguous snapshot archive")
		}
		result = append(result, op)
		offset += length
	}
	if offset != len(payload) {
		return nil, errors.New("account: trailing snapshot archive data")
	}
	return result, nil
}

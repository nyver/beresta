package server

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
)

type Snapshot struct {
	Protocol        string     `json:"protocol"`
	SchemaVersion   int        `json:"schema_version"`
	ID              string     `json:"snapshot_id"`
	WorkspaceID     string     `json:"workspace_id"`
	BaseSequence    int64      `json:"base_seq"`
	CursorEpoch     int64      `json:"cursor_epoch"`
	KeyID           string     `json:"key_id"`
	CreatorDeviceID string     `json:"creator_device_id"`
	HLCPhysicalMS   int64      `json:"hlc_physical_ms"`
	HLCLogical      uint32     `json:"hlc_logical"`
	Nonce           []byte     `json:"nonce"`
	CiphertextHash  []byte     `json:"ciphertext_hash"`
	Ciphertext      []byte     `json:"ciphertext,omitempty"`
	Signature       []byte     `json:"sig"`
	EligibleAt      *time.Time `json:"eligible_at,omitempty"`
}

type SnapshotAck struct {
	Protocol       string `json:"protocol"`
	SchemaVersion  int    `json:"schema_version"`
	SnapshotID     string `json:"snapshot_id"`
	WorkspaceID    string `json:"workspace_id"`
	DeviceID       string `json:"device_id"`
	BaseSequence   int64  `json:"base_seq"`
	CiphertextHash []byte `json:"ciphertext_hash"`
	Signature      []byte `json:"sig"`
}

func (s *Storage) PutSnapshot(ctx context.Context, principal Principal, snapshot Snapshot, now time.Time) error {
	if err := validateSnapshot(s.config, snapshot, now); err != nil {
		return err
	}
	if snapshot.CreatorDeviceID != principal.DeviceID {
		return ErrForbidden
	}
	digest := sha256.Sum256(snapshot.Ciphertext)
	if !equalBytes(digest[:], snapshot.CiphertextHash) {
		return fmt.Errorf("%w: snapshot ciphertext hash mismatch", ErrInvalid)
	}
	_, err := withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		member, err := s.isActiveMember(ctx, transaction, principal.UserID, snapshot.WorkspaceID)
		if err != nil || !member {
			if err == nil {
				err = ErrForbidden
			}
			return struct{}{}, err
		}
		var public []byte
		if err := transaction.QueryRowContext(ctx, `SELECT signing_public FROM devices
			WHERE device_id = ? AND user_id = ? AND revoked_at IS NULL`, principal.DeviceID, principal.UserID).Scan(&public); err != nil {
			return struct{}{}, ErrForbidden
		}
		payload, err := snapshotSignaturePayload(snapshot)
		if err != nil {
			return struct{}{}, fmt.Errorf("%w: invalid canonical snapshot fields", ErrInvalid)
		}
		if err := corecrypto.VerifyCanonical(corecrypto.CryptoProfileV1, public, corecrypto.SignatureDomainSnapshot, payload, snapshot.Signature); err != nil {
			return struct{}{}, ErrUnauthorized
		}
		var latest, epoch int64
		var currentKeyID string
		if err := transaction.QueryRowContext(ctx, `SELECT latest_seq, cursor_epoch, current_key_id FROM workspaces WHERE workspace_id = ?`, snapshot.WorkspaceID).
			Scan(&latest, &epoch, &currentKeyID); err != nil {
			return struct{}{}, err
		}
		if snapshot.BaseSequence > latest || snapshot.CursorEpoch != epoch {
			return struct{}{}, fmt.Errorf("%w: snapshot base cursor is unavailable", ErrConflict)
		}
		if snapshot.KeyID != currentKeyID {
			return struct{}{}, fmt.Errorf("%w: snapshot does not use the current workspace key", ErrConflict)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO snapshots(
				snapshot_id, workspace_id, base_seq, cursor_epoch, key_id, creator_device_id,
				hlc_physical_ms, hlc_logical, nonce, ciphertext_hash, ciphertext, signature, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			snapshot.ID, snapshot.WorkspaceID, snapshot.BaseSequence, snapshot.CursorEpoch, snapshot.KeyID,
			snapshot.CreatorDeviceID, snapshot.HLCPhysicalMS, snapshot.HLCLogical, snapshot.Nonce,
			snapshot.CiphertextHash, snapshot.Ciphertext, snapshot.Signature, unixNow(now)); err != nil {
			return struct{}{}, classifyConstraint(err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Storage) AcknowledgeSnapshot(ctx context.Context, principal Principal, ack SnapshotAck, now time.Time) (bool, error) {
	if err := validateID(ack.SnapshotID, "snapshot_id"); err != nil {
		return false, err
	}
	if err := validateID(ack.WorkspaceID, "workspace_id"); err != nil {
		return false, err
	}
	if ack.Protocol != "beresta.sync.v1" || ack.SchemaVersion != 1 || ack.DeviceID != principal.DeviceID ||
		ack.BaseSequence < 0 || len(ack.CiphertextHash) != sha256.Size || len(ack.Signature) != ed25519.SignatureSize {
		return false, ErrForbidden
	}
	return withWriteTx(ctx, s, func(transaction *sql.Tx) (bool, error) {
		member, err := s.isActiveMember(ctx, transaction, principal.UserID, ack.WorkspaceID)
		if err != nil || !member {
			if err == nil {
				err = ErrForbidden
			}
			return false, err
		}
		var base int64
		var hash []byte
		if err := transaction.QueryRowContext(ctx, `SELECT base_seq, ciphertext_hash FROM snapshots
			WHERE snapshot_id = ? AND workspace_id = ?`, ack.SnapshotID, ack.WorkspaceID).Scan(&base, &hash); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, ErrNotFound
			}
			return false, err
		}
		if base != ack.BaseSequence || !equalBytes(hash, ack.CiphertextHash) {
			return false, fmt.Errorf("%w: snapshot acknowledgement metadata mismatch", ErrConflict)
		}
		var public []byte
		if err := transaction.QueryRowContext(ctx, `SELECT signing_public FROM devices
			WHERE device_id = ? AND user_id = ? AND revoked_at IS NULL`, principal.DeviceID, principal.UserID).Scan(&public); err != nil {
			return false, ErrForbidden
		}
		payload, err := snapshotAckSignaturePayload(ack)
		if err != nil {
			return false, fmt.Errorf("%w: invalid canonical snapshot acknowledgement", ErrInvalid)
		}
		if err := corecrypto.VerifyCanonical(corecrypto.CryptoProfileV1, public, corecrypto.SignatureDomainSnapshotAck, payload, ack.Signature); err != nil {
			return false, ErrUnauthorized
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO snapshot_acknowledgements(snapshot_id, device_id, ciphertext_hash, signature, created_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(snapshot_id, device_id) DO UPDATE SET
				ciphertext_hash = excluded.ciphertext_hash, signature = excluded.signature, created_at = excluded.created_at`,
			ack.SnapshotID, principal.DeviceID, ack.CiphertextHash, ack.Signature, unixNow(now)); err != nil {
			return false, err
		}
		eligible, err := snapshotEligible(ctx, transaction, ack.SnapshotID, ack.WorkspaceID, now)
		if err != nil {
			return false, err
		}
		if eligible {
			if _, err := transaction.ExecContext(ctx, `UPDATE snapshots SET eligible_at = COALESCE(eligible_at, ?) WHERE snapshot_id = ?`, unixNow(now), ack.SnapshotID); err != nil {
				return false, err
			}
		}
		return eligible, nil
	})
}

func (s *Storage) ListSnapshots(ctx context.Context, principal Principal, workspaceID string) ([]Snapshot, error) {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return nil, err
	}
	member, err := s.isActiveMember(ctx, s.db, principal.UserID, workspaceID)
	if err != nil || !member {
		if err == nil {
			err = ErrForbidden
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT snapshot_id, base_seq, cursor_epoch, key_id, creator_device_id, hlc_physical_ms,
		       hlc_logical, nonce, ciphertext_hash, signature, eligible_at
		FROM snapshots WHERE workspace_id = ? ORDER BY base_seq DESC, created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Snapshot
	for rows.Next() {
		item := Snapshot{Protocol: "beresta.sync.v1", SchemaVersion: 1, WorkspaceID: workspaceID}
		var eligible sql.NullInt64
		if err := rows.Scan(&item.ID, &item.BaseSequence, &item.CursorEpoch, &item.KeyID, &item.CreatorDeviceID,
			&item.HLCPhysicalMS, &item.HLCLogical, &item.Nonce, &item.CiphertextHash, &item.Signature, &eligible); err != nil {
			return nil, err
		}
		item.EligibleAt = nullableTime(eligible)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Storage) GetSnapshot(ctx context.Context, principal Principal, snapshotID string) (Snapshot, error) {
	if err := validateID(snapshotID, "snapshot_id"); err != nil {
		return Snapshot{}, err
	}
	var item Snapshot
	var eligible sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT snapshot_id, workspace_id, base_seq, cursor_epoch, key_id, creator_device_id,
		       hlc_physical_ms, hlc_logical, nonce, ciphertext_hash, ciphertext, signature, eligible_at
		FROM snapshots WHERE snapshot_id = ?`, snapshotID).Scan(&item.ID, &item.WorkspaceID, &item.BaseSequence,
		&item.CursorEpoch, &item.KeyID, &item.CreatorDeviceID, &item.HLCPhysicalMS, &item.HLCLogical,
		&item.Nonce, &item.CiphertextHash, &item.Ciphertext, &item.Signature, &eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, err
	}
	item.Protocol = "beresta.sync.v1"
	item.SchemaVersion = 1
	member, err := s.isActiveMember(ctx, s.db, principal.UserID, item.WorkspaceID)
	if err != nil || !member {
		return Snapshot{}, ErrForbidden
	}
	item.EligibleAt = nullableTime(eligible)
	return item, nil
}

// LatestSnapshot returns the newest snapshot for workspaceID, ciphertext
// included. It orders inside SQL rather than relying on a list helper's
// ordering, so the "latest" contract does not silently change if that
// helper's ORDER BY ever does.
func (s *Storage) LatestSnapshot(ctx context.Context, principal Principal, workspaceID string) (Snapshot, error) {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return Snapshot{}, err
	}
	member, err := s.isActiveMember(ctx, s.db, principal.UserID, workspaceID)
	if err != nil || !member {
		if err == nil {
			err = ErrForbidden
		}
		return Snapshot{}, err
	}
	item := Snapshot{Protocol: "beresta.sync.v1", SchemaVersion: 1, WorkspaceID: workspaceID}
	var eligible sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
		SELECT snapshot_id, base_seq, cursor_epoch, key_id, creator_device_id,
		       hlc_physical_ms, hlc_logical, nonce, ciphertext_hash, ciphertext, signature, eligible_at
		FROM snapshots WHERE workspace_id = ?
		ORDER BY base_seq DESC, created_at DESC LIMIT 1`, workspaceID).Scan(&item.ID, &item.BaseSequence,
		&item.CursorEpoch, &item.KeyID, &item.CreatorDeviceID, &item.HLCPhysicalMS, &item.HLCLogical,
		&item.Nonce, &item.CiphertextHash, &item.Ciphertext, &item.Signature, &eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, err
	}
	item.EligibleAt = nullableTime(eligible)
	return item, nil
}

func validateSnapshot(cfg Config, snapshot Snapshot, now time.Time) error {
	if snapshot.Protocol != "beresta.sync.v1" || snapshot.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported snapshot version", ErrInvalid)
	}
	for field, value := range map[string]string{"snapshot_id": snapshot.ID, "workspace_id": snapshot.WorkspaceID, "creator_device_id": snapshot.CreatorDeviceID} {
		if err := validateID(value, field); err != nil {
			return err
		}
	}
	if err := validateOpaqueID(snapshot.KeyID, "key_id"); err != nil {
		return err
	}
	if snapshot.BaseSequence < 0 || snapshot.CursorEpoch <= 0 || snapshot.CursorEpoch > math.MaxUint32 ||
		snapshot.HLCPhysicalMS < now.Add(-cfg.Limits.MaxHLCPastAge.Value()).UnixMilli() ||
		snapshot.HLCPhysicalMS > now.Add(cfg.Limits.MaxHLCFutureSkew.Value()).UnixMilli() || len(snapshot.Nonce) != 24 ||
		len(snapshot.CiphertextHash) != sha256.Size || len(snapshot.Ciphertext) < corecrypto.AEADTagBytes ||
		int64(len(snapshot.Ciphertext)) > cfg.Limits.MaxBlobBytes || len(snapshot.Ciphertext) > corecrypto.MaxSnapshotCiphertextBytes ||
		len(snapshot.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: invalid snapshot envelope", ErrInvalid)
	}
	return nil
}

func snapshotEligible(ctx context.Context, transaction *sql.Tx, snapshotID, workspaceID string, now time.Time) (bool, error) {
	var missingActive int
	if err := transaction.QueryRowContext(ctx, `
		SELECT count(*) FROM devices d
		JOIN memberships m ON m.user_id = d.user_id AND m.workspace_id = ? AND m.revoked_at IS NULL
		LEFT JOIN snapshot_acknowledgements a ON a.device_id = d.device_id AND a.snapshot_id = ?
		WHERE d.revoked_at IS NULL AND a.device_id IS NULL`, workspaceID, snapshotID).Scan(&missingActive); err != nil {
		return false, err
	}
	var recentRevoked int
	retentionBoundary := unixNow(now.Add(-30 * 24 * time.Hour))
	if err := transaction.QueryRowContext(ctx, `
		SELECT count(*) FROM devices d
		JOIN memberships m ON m.user_id = d.user_id AND m.workspace_id = ?
		WHERE d.revoked_at IS NOT NULL AND d.revoked_at > ?`, workspaceID, retentionBoundary).Scan(&recentRevoked); err != nil {
		return false, err
	}
	var recentRevokedMembers int
	if err := transaction.QueryRowContext(ctx, `
		SELECT count(*) FROM memberships
		WHERE workspace_id = ? AND revoked_at IS NOT NULL AND revoked_at > ?`, workspaceID, retentionBoundary).
		Scan(&recentRevokedMembers); err != nil {
		return false, err
	}
	return missingActive == 0 && recentRevoked == 0 && recentRevokedMembers == 0, nil
}

func snapshotSignaturePayload(snapshot Snapshot) ([]byte, error) {
	snapshotID, err := decodeCanonicalID(snapshot.ID)
	if err != nil {
		return nil, err
	}
	workspaceID, err := decodeCanonicalID(snapshot.WorkspaceID)
	if err != nil {
		return nil, err
	}
	deviceID, err := decodeCanonicalID(snapshot.CreatorDeviceID)
	if err != nil {
		return nil, err
	}
	keyID, err := decodeOpaqueID(snapshot.KeyID)
	if err != nil {
		return nil, err
	}
	return corecrypto.CanonicalSnapshotSignatureInput(corecrypto.SnapshotSignatureFields{
		SnapshotID: snapshotID, WorkspaceID: workspaceID, BaseSequence: uint64(snapshot.BaseSequence),
		CursorEpoch: uint32(snapshot.CursorEpoch), KeyID: keyID, CreatorDeviceID: deviceID,
		HLCPhysicalMS: uint64(snapshot.HLCPhysicalMS), HLCLogical: snapshot.HLCLogical, HLCDeviceID: deviceID,
		Nonce: snapshot.Nonce, CiphertextHash: snapshot.CiphertextHash, Ciphertext: snapshot.Ciphertext,
	})
}

func snapshotSignatureInput(snapshot Snapshot) []byte {
	payload, err := snapshotSignaturePayload(snapshot)
	if err != nil {
		return nil
	}
	return canonicalSigningInput(corecrypto.SignatureDomainSnapshot, payload)
}

func snapshotAckSignaturePayload(ack SnapshotAck) ([]byte, error) {
	snapshotID, err := decodeCanonicalID(ack.SnapshotID)
	if err != nil {
		return nil, err
	}
	workspaceID, err := decodeCanonicalID(ack.WorkspaceID)
	if err != nil {
		return nil, err
	}
	deviceID, err := decodeCanonicalID(ack.DeviceID)
	if err != nil {
		return nil, err
	}
	return corecrypto.CanonicalSnapshotAckSignatureInput(corecrypto.SnapshotAckSignatureFields{
		SnapshotID: snapshotID, WorkspaceID: workspaceID, DeviceID: deviceID,
		BaseSequence: uint64(ack.BaseSequence), CiphertextHash: ack.CiphertextHash,
	})
}

func snapshotAckSignatureInput(ack SnapshotAck) []byte {
	payload, err := snapshotAckSignaturePayload(ack)
	if err != nil {
		return nil
	}
	return canonicalSigningInput(corecrypto.SignatureDomainSnapshotAck, payload)
}

func canonicalSigningInput(domain corecrypto.SignatureDomain, payload []byte) []byte {
	domainBytes := []byte(domain)
	result := binary.BigEndian.AppendUint32(nil, uint32(len(domainBytes)))
	result = append(result, domainBytes...)
	return append(result, payload...)
}

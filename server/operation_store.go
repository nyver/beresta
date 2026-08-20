package server

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
)

const maxOperationBatchCiphertextBytes = 48 << 20

type Operation struct {
	Protocol      string `json:"protocol"`
	SchemaVersion int    `json:"schema_version"`
	OpID          string `json:"op_id"`
	WorkspaceID   string `json:"workspace_id"`
	DeviceID      string `json:"device_id"`
	Sequence      int64  `json:"seq,omitempty"`
	HLCPhysicalMS int64  `json:"hlc_physical_ms"`
	HLCLogical    uint32 `json:"hlc_logical"`
	KeyID         string `json:"key_id"`
	Nonce         []byte `json:"nonce"`
	Ciphertext    []byte `json:"ciphertext"`
	Signature     []byte `json:"sig"`
}

type PushResult struct {
	OpID      string `json:"op_id"`
	Sequence  int64  `json:"seq"`
	Duplicate bool   `json:"duplicate"`
}

type Changes struct {
	WorkspaceID string      `json:"workspace_id"`
	Cursor      int64       `json:"cursor"`
	CursorEpoch int64       `json:"cursor_epoch"`
	Operations  []Operation `json:"operations"`
}

func (s *Storage) PushOperations(ctx context.Context, principal Principal, operations []Operation, now time.Time) ([]PushResult, error) {
	if len(operations) == 0 || len(operations) > s.config.Limits.MaxOperationsPerBatch {
		return nil, fmt.Errorf("%w: operation batch size is invalid", ErrInvalid)
	}
	var batchBytes int64
	for _, operation := range operations {
		batchBytes += int64(len(operation.Ciphertext))
		if batchBytes > maxOperationBatchCiphertextBytes {
			return nil, fmt.Errorf("%w: operation batch byte size is invalid", ErrInvalid)
		}
	}
	return withWriteTx(ctx, s, func(transaction *sql.Tx) ([]PushResult, error) {
		results := make([]PushResult, 0, len(operations))
		for _, operation := range operations {
			result, err := s.pushOperation(ctx, transaction, principal, operation, now)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
		return results, nil
	})
}

func (s *Storage) pushOperation(ctx context.Context, transaction *sql.Tx, principal Principal, operation Operation, now time.Time) (PushResult, error) {
	if err := validateOperation(s.config, operation, now); err != nil {
		return PushResult{}, err
	}
	if operation.DeviceID != principal.DeviceID {
		return PushResult{}, ErrForbidden
	}
	member, err := s.isActiveMember(ctx, transaction, principal.UserID, operation.WorkspaceID)
	if err != nil || !member {
		if err == nil {
			err = ErrForbidden
		}
		return PushResult{}, err
	}
	var publicKey []byte
	err = transaction.QueryRowContext(ctx, `
		SELECT signing_public FROM devices WHERE device_id = ? AND user_id = ? AND revoked_at IS NULL`,
		operation.DeviceID, principal.UserID).Scan(&publicKey)
	if errors.Is(err, sql.ErrNoRows) {
		return PushResult{}, ErrForbidden
	}
	if err != nil {
		return PushResult{}, err
	}
	payload, err := operationSignaturePayload(operation)
	if err != nil {
		return PushResult{}, fmt.Errorf("%w: invalid canonical operation fields", ErrInvalid)
	}
	if err := corecrypto.VerifyCanonical(corecrypto.CryptoProfileV1, publicKey, corecrypto.SignatureDomainOperation, payload, operation.Signature); err != nil {
		return PushResult{}, ErrUnauthorized
	}
	envelopeDigest := sha256.Sum256(append(append([]byte(nil), payload...), operation.Signature...))
	var existingSequence int64
	var existingHash []byte
	err = transaction.QueryRowContext(ctx, `SELECT seq, envelope_hash FROM operations WHERE op_id = ?`, operation.OpID).
		Scan(&existingSequence, &existingHash)
	if err == nil {
		if !equalBytes(existingHash, envelopeDigest[:]) {
			return PushResult{}, fmt.Errorf("%w: operation identifier was reused with different content", ErrConflict)
		}
		return PushResult{OpID: operation.OpID, Sequence: existingSequence, Duplicate: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PushResult{}, err
	}
	var latest int64
	var currentKeyID string
	if err := transaction.QueryRowContext(ctx, `SELECT latest_seq, current_key_id FROM workspaces WHERE workspace_id = ?`, operation.WorkspaceID).
		Scan(&latest, &currentKeyID); err != nil {
		return PushResult{}, err
	}
	if operation.KeyID != currentKeyID {
		return PushResult{}, fmt.Errorf("%w: operation does not use the current workspace key", ErrConflict)
	}
	sequence := latest + 1
	if _, err := transaction.ExecContext(ctx, `UPDATE workspaces SET latest_seq = ? WHERE workspace_id = ?`, sequence, operation.WorkspaceID); err != nil {
		return PushResult{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO operations(
			op_id, workspace_id, device_id, seq, hlc_physical_ms, hlc_logical,
			key_id, nonce, ciphertext, signature, envelope_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.OpID, operation.WorkspaceID, operation.DeviceID, sequence,
		operation.HLCPhysicalMS, operation.HLCLogical, operation.KeyID, operation.Nonce,
		operation.Ciphertext, operation.Signature, envelopeDigest[:], unixNow(now)); err != nil {
		return PushResult{}, classifyConstraint(err)
	}
	return PushResult{OpID: operation.OpID, Sequence: sequence}, nil
}

func validateOperation(cfg Config, operation Operation, now time.Time) error {
	if operation.Protocol != "beresta.sync.v1" || operation.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported operation version", ErrInvalid)
	}
	for field, value := range map[string]string{"op_id": operation.OpID, "workspace_id": operation.WorkspaceID, "device_id": operation.DeviceID} {
		if err := validateID(value, field); err != nil {
			return err
		}
	}
	if err := validateOpaqueID(operation.KeyID, "key_id"); err != nil {
		return err
	}
	if operation.Sequence != 0 || operation.HLCPhysicalMS < now.Add(-cfg.Limits.MaxHLCPastAge.Value()).UnixMilli() ||
		operation.HLCPhysicalMS > now.Add(cfg.Limits.MaxHLCFutureSkew.Value()).UnixMilli() ||
		len(operation.Nonce) != 24 || len(operation.Signature) != ed25519.SignatureSize || len(operation.Ciphertext) < corecrypto.AEADTagBytes ||
		int64(len(operation.Ciphertext)) > cfg.Limits.MaxOperationBytes {
		return fmt.Errorf("%w: invalid operation envelope", ErrInvalid)
	}
	return nil
}

func (s *Storage) PullChanges(ctx context.Context, principal Principal, workspaceID string, cursor int64, limit int) (Changes, error) {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return Changes{}, err
	}
	if cursor < 0 || limit <= 0 || limit > s.config.Limits.MaxOperationsPerBatch {
		return Changes{}, fmt.Errorf("%w: invalid cursor or limit", ErrInvalid)
	}
	member, err := s.isActiveMember(ctx, s.db, principal.UserID, workspaceID)
	if err != nil || !member {
		if err == nil {
			err = ErrForbidden
		}
		return Changes{}, err
	}
	var latest, epoch int64
	if err := s.db.QueryRowContext(ctx, `SELECT latest_seq, cursor_epoch FROM workspaces WHERE workspace_id = ?`, workspaceID).Scan(&latest, &epoch); err != nil {
		return Changes{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT op_id, device_id, seq, hlc_physical_ms, hlc_logical, key_id, nonce, ciphertext, signature
		FROM operations WHERE workspace_id = ? AND seq > ? ORDER BY seq LIMIT ?`, workspaceID, cursor, limit)
	if err != nil {
		return Changes{}, err
	}
	defer rows.Close()
	result := Changes{WorkspaceID: workspaceID, Cursor: cursor, CursorEpoch: epoch}
	var responseBytes int64
	for rows.Next() {
		operation := Operation{Protocol: "beresta.sync.v1", SchemaVersion: 1, WorkspaceID: workspaceID}
		if err := rows.Scan(&operation.OpID, &operation.DeviceID, &operation.Sequence, &operation.HLCPhysicalMS,
			&operation.HLCLogical, &operation.KeyID, &operation.Nonce, &operation.Ciphertext, &operation.Signature); err != nil {
			return Changes{}, err
		}
		if responseBytes+int64(len(operation.Ciphertext)) > maxOperationBatchCiphertextBytes {
			break
		}
		responseBytes += int64(len(operation.Ciphertext))
		result.Operations = append(result.Operations, operation)
		result.Cursor = operation.Sequence
	}
	if err := rows.Err(); err != nil {
		return Changes{}, err
	}
	if len(result.Operations) == 0 && cursor > latest {
		return Changes{}, fmt.Errorf("%w: cursor is ahead of workspace", ErrConflict)
	}
	return result, nil
}

func operationSignaturePayload(operation Operation) ([]byte, error) {
	opID, err := decodeCanonicalID(operation.OpID)
	if err != nil {
		return nil, err
	}
	workspaceID, err := decodeCanonicalID(operation.WorkspaceID)
	if err != nil {
		return nil, err
	}
	deviceID, err := decodeCanonicalID(operation.DeviceID)
	if err != nil {
		return nil, err
	}
	keyID, err := decodeOpaqueID(operation.KeyID)
	if err != nil {
		return nil, err
	}
	return corecrypto.CanonicalOperationSignatureInput(corecrypto.OperationSignatureFields{
		OpID: opID, WorkspaceID: workspaceID, DeviceID: deviceID,
		HLCPhysicalMS: uint64(operation.HLCPhysicalMS), HLCLogical: operation.HLCLogical, HLCDeviceID: deviceID,
		KeyID: keyID, Nonce: operation.Nonce, Ciphertext: operation.Ciphertext,
	})
}

func operationSignatureInput(operation Operation) []byte {
	payload, err := operationSignaturePayload(operation)
	if err != nil {
		return nil
	}
	domain := []byte(corecrypto.SignatureDomainOperation)
	result := binary.BigEndian.AppendUint32(nil, uint32(len(domain)))
	result = append(result, domain...)
	return append(result, payload...)
}

func appendLengthFields(destination []byte, fields ...[]byte) []byte {
	for _, field := range fields {
		destination = binary.BigEndian.AppendUint32(destination, uint32(len(field)))
		destination = append(destination, field...)
	}
	return destination
}

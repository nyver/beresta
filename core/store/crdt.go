package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

// CRDTState is one note's persisted Yjs document state: an encrypted
// snapshot blob (see corecrypto.EncryptAndPackObject) and its plaintext
// state vector.
type CRDTState struct {
	Snapshot    []byte
	StateVector []byte
}

// LoadCRDTState returns a note's persisted CRDT state. ok is false when the
// note has never had a body command applied (a brand-new note with an empty
// body).
func LoadCRDTState(ctx context.Context, exec Executor, noteID model.ID) (state CRDTState, ok bool, err error) {
	err = exec.QueryRowContext(ctx,
		`SELECT snapshot, state_vector FROM crdt_states WHERE note_id = ?`, noteID.Bytes(),
	).Scan(&state.Snapshot, &state.StateVector)
	if errors.Is(err, sql.ErrNoRows) {
		return CRDTState{}, false, nil
	}
	if err != nil {
		return CRDTState{}, false, fmt.Errorf("store: load CRDT state: %w", err)
	}
	return state, true, nil
}

// UpsertCRDTState replaces a note's persisted CRDT state with the result of
// applying its latest command.
func UpsertCRDTState(ctx context.Context, exec Executor, noteID model.ID, state CRDTState, updatedUnixMS uint64) error {
	_, err := exec.ExecContext(ctx,
		`INSERT INTO crdt_states (note_id, snapshot, state_vector, updated_unix_ms) VALUES (?, ?, ?, ?)
		 ON CONFLICT (note_id) DO UPDATE SET snapshot = excluded.snapshot, state_vector = excluded.state_vector, updated_unix_ms = excluded.updated_unix_ms`,
		noteID.Bytes(), state.Snapshot, state.StateVector, updatedUnixMS,
	)
	if err != nil {
		return fmt.Errorf("store: upsert CRDT state: %w", err)
	}
	return nil
}

// InsertCRDTUpdate appends one applied Yjs update to a note's update log.
// Plaintext CRDT update bytes carry no key material, so unlike crdt_states
// and revisions this log is not separately encrypted: it lives inside the
// already-encrypted-at-rest SQLCipher database file. The log is a companion
// audit trail alongside crdt_states' materialized current snapshot; nothing
// currently reconstructs state from it, so callers do not need to prune it
// themselves.
func InsertCRDTUpdate(ctx context.Context, exec Executor, noteID model.ID, updateBytes []byte, originDeviceID model.ID, createdUnixMS uint64) error {
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO crdt_updates (note_id, update_bytes, origin_device_id, created_unix_ms) VALUES (?, ?, ?, ?)`,
		noteID.Bytes(), updateBytes, originDeviceID.Bytes(), createdUnixMS,
	); err != nil {
		return fmt.Errorf("store: insert CRDT update: %w", err)
	}
	return nil
}

package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Keybag struct {
	Version    int64  `json:"version"`
	Ciphertext []byte `json:"ciphertext"`
}

type Workspace struct {
	ID           string `json:"workspace_id"`
	OwnerUserID  string `json:"owner_user_id"`
	CurrentKeyID string `json:"current_key_id"`
	LatestSeq    int64  `json:"latest_seq"`
	CursorEpoch  int64  `json:"cursor_epoch"`
}

type Member struct {
	UserID          string     `json:"user_id"`
	DisplayName     string     `json:"display_name"`
	Role            string     `json:"role"`
	IdentityPublic  []byte     `json:"identity_public"`
	AuthorityPublic []byte     `json:"authority_public"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

type Device struct {
	ID          string     `json:"device_id"`
	UserID      string     `json:"user_id"`
	DisplayName string     `json:"display_name"`
	PublicKey   []byte     `json:"signing_public"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type KeyEnvelope struct {
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	KeyID       string    `json:"key_id"`
	Envelope    []byte    `json:"envelope"`
	CreatedAt   time.Time `json:"created_at"`
}

type KeyEnvelopeInput struct {
	UserID   string `json:"user_id"`
	Envelope []byte `json:"envelope"`
}

func (s *Storage) GetKeybag(ctx context.Context, principal Principal) (Keybag, error) {
	var result Keybag
	err := s.db.QueryRowContext(ctx, `SELECT version, ciphertext FROM keybags WHERE user_id = ?`, principal.UserID).
		Scan(&result.Version, &result.Ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return Keybag{}, ErrNotFound
	}
	return result, err
}

func (s *Storage) PutKeybag(ctx context.Context, principal Principal, expectedVersion int64, ciphertext []byte, now time.Time) (Keybag, error) {
	if expectedVersion <= 0 || len(ciphertext) == 0 || int64(len(ciphertext)) > s.config.Limits.MaxOperationBytes*4 {
		return Keybag{}, fmt.Errorf("%w: invalid keybag update", ErrInvalid)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Keybag{}, err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `
		UPDATE keybags SET version = version + 1, ciphertext = ?, updated_at = ?
		WHERE user_id = ? AND version = ?`, ciphertext, unixNow(now), principal.UserID, expectedVersion)
	if err != nil {
		return Keybag{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Keybag{}, err
	}
	if rows != 1 {
		var current Keybag
		if err := transaction.QueryRowContext(ctx, `SELECT version, ciphertext FROM keybags WHERE user_id = ?`, principal.UserID).
			Scan(&current.Version, &current.Ciphertext); err != nil {
			return Keybag{}, err
		}
		return current, ErrConflict
	}
	if err := transaction.Commit(); err != nil {
		return Keybag{}, err
	}
	return Keybag{Version: expectedVersion + 1, Ciphertext: ciphertext}, nil
}

func (s *Storage) ListWorkspaces(ctx context.Context, principal Principal) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.workspace_id, w.owner_user_id, w.current_key_id, w.latest_seq, w.cursor_epoch
		FROM workspaces w JOIN memberships m ON m.workspace_id = w.workspace_id
		WHERE m.user_id = ? AND m.revoked_at IS NULL ORDER BY w.workspace_id`, principal.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var workspaces []Workspace
	for rows.Next() {
		var workspace Workspace
		if err := rows.Scan(&workspace.ID, &workspace.OwnerUserID, &workspace.CurrentKeyID, &workspace.LatestSeq, &workspace.CursorEpoch); err != nil {
			return nil, err
		}
		workspaces = append(workspaces, workspace)
	}
	return workspaces, rows.Err()
}

func (s *Storage) CreateWorkspace(ctx context.Context, principal Principal, workspaceID, keyID string, envelope []byte, now time.Time) (Workspace, error) {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return Workspace{}, err
	}
	if err := validateOpaqueID(keyID, "key_id"); err != nil {
		return Workspace{}, err
	}
	if len(envelope) == 0 || int64(len(envelope)) > s.config.Limits.MaxOperationBytes {
		return Workspace{}, fmt.Errorf("%w: key envelope is empty", ErrInvalid)
	}
	return withWriteTx(ctx, s, func(transaction *sql.Tx) (Workspace, error) {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO workspaces(workspace_id, owner_user_id, current_key_id, created_at) VALUES (?, ?, ?, ?)`,
			workspaceID, principal.UserID, keyID, unixNow(now)); err != nil {
			return Workspace{}, classifyConstraint(err)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO memberships(workspace_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
			workspaceID, principal.UserID, unixNow(now)); err != nil {
			return Workspace{}, err
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO key_envelopes(workspace_id, user_id, key_id, envelope, created_at) VALUES (?, ?, ?, ?, ?)`,
			workspaceID, principal.UserID, keyID, envelope, unixNow(now)); err != nil {
			return Workspace{}, err
		}
		return Workspace{ID: workspaceID, OwnerUserID: principal.UserID, CurrentKeyID: keyID, CursorEpoch: 1}, nil
	})
}

func (s *Storage) ListMembers(ctx context.Context, principal Principal, workspaceID string) ([]Member, error) {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return nil, err
	}
	member, err := s.isActiveMember(ctx, s.db, principal.UserID, workspaceID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.user_id, u.display_name, m.role, u.identity_public, u.authority_public, m.revoked_at
		FROM memberships m JOIN users u ON u.user_id = m.user_id
		WHERE m.workspace_id = ? ORDER BY m.user_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Member
	for rows.Next() {
		var item Member
		var revoked sql.NullInt64
		if err := rows.Scan(&item.UserID, &item.DisplayName, &item.Role, &item.IdentityPublic, &item.AuthorityPublic, &revoked); err != nil {
			return nil, err
		}
		item.RevokedAt = nullableTime(revoked)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Storage) AddMember(ctx context.Context, principal Principal, workspaceID, userID, keyID string, envelope []byte, now time.Time) error {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return err
	}
	if err := validateID(userID, "user_id"); err != nil {
		return err
	}
	if err := validateOpaqueID(keyID, "key_id"); err != nil {
		return err
	}
	if len(envelope) == 0 || int64(len(envelope)) > s.config.Limits.MaxOperationBytes {
		return fmt.Errorf("%w: key envelope is empty", ErrInvalid)
	}
	_, err := withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		if err := requireWorkspaceOwner(ctx, transaction, principal.UserID, workspaceID); err != nil {
			return struct{}{}, err
		}
		var currentKeyID string
		if err := transaction.QueryRowContext(ctx, `SELECT current_key_id FROM workspaces WHERE workspace_id = ?`, workspaceID).Scan(&currentKeyID); err != nil {
			return struct{}{}, err
		}
		if currentKeyID != keyID {
			return struct{}{}, fmt.Errorf("%w: envelope must use the current workspace key", ErrConflict)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO memberships(workspace_id, user_id, role, created_at) VALUES (?, ?, 'member', ?)
			ON CONFLICT(workspace_id, user_id) DO UPDATE SET revoked_at = NULL`, workspaceID, userID, unixNow(now)); err != nil {
			return struct{}{}, classifyConstraint(err)
		}
		_, err := transaction.ExecContext(ctx, `
			INSERT INTO key_envelopes(workspace_id, user_id, key_id, envelope, created_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(workspace_id, user_id, key_id) DO UPDATE SET envelope = excluded.envelope, created_at = excluded.created_at`,
			workspaceID, userID, keyID, envelope, unixNow(now))
		return struct{}{}, err
	})
	return err
}

func (s *Storage) GetKeyEnvelopes(ctx context.Context, principal Principal, workspaceID string) ([]KeyEnvelope, error) {
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
		SELECT workspace_id, user_id, key_id, envelope, created_at FROM key_envelopes
		WHERE workspace_id = ? AND user_id = ? ORDER BY created_at, key_id`, workspaceID, principal.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []KeyEnvelope
	for rows.Next() {
		var item KeyEnvelope
		var created int64
		if err := rows.Scan(&item.WorkspaceID, &item.UserID, &item.KeyID, &item.Envelope, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = time.Unix(created, 0).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Storage) RotateWorkspaceKey(ctx context.Context, principal Principal, workspaceID, keyID string, envelopes []KeyEnvelopeInput, now time.Time) error {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return err
	}
	if err := validateOpaqueID(keyID, "key_id"); err != nil {
		return err
	}
	if len(envelopes) == 0 || len(envelopes) > 5 {
		return fmt.Errorf("%w: key rotation must cover every active member", ErrInvalid)
	}
	seen := make(map[string]bool, len(envelopes))
	for _, envelope := range envelopes {
		if err := validateID(envelope.UserID, "user_id"); err != nil {
			return err
		}
		if seen[envelope.UserID] || len(envelope.Envelope) == 0 || int64(len(envelope.Envelope)) > s.config.Limits.MaxOperationBytes {
			return fmt.Errorf("%w: invalid or duplicate key envelope", ErrInvalid)
		}
		seen[envelope.UserID] = true
	}
	_, err := withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		if err := requireWorkspaceOwner(ctx, transaction, principal.UserID, workspaceID); err != nil {
			return struct{}{}, err
		}
		rows, err := transaction.QueryContext(ctx, `SELECT user_id FROM memberships WHERE workspace_id = ? AND revoked_at IS NULL`, workspaceID)
		if err != nil {
			return struct{}{}, err
		}
		var active []string
		for rows.Next() {
			var userID string
			if err := rows.Scan(&userID); err != nil {
				rows.Close()
				return struct{}{}, err
			}
			active = append(active, userID)
		}
		iterationErr := rows.Err()
		if err := rows.Close(); err != nil {
			return struct{}{}, err
		}
		if iterationErr != nil {
			return struct{}{}, iterationErr
		}
		if len(active) != len(envelopes) {
			return struct{}{}, fmt.Errorf("%w: key rotation must cover every active member", ErrConflict)
		}
		for _, userID := range active {
			if !seen[userID] {
				return struct{}{}, fmt.Errorf("%w: key rotation must cover every active member", ErrConflict)
			}
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE workspaces SET current_key_id = ? WHERE workspace_id = ?`, keyID, workspaceID); err != nil {
			return struct{}{}, err
		}
		for _, envelope := range envelopes {
			if _, err := transaction.ExecContext(ctx, `
				INSERT INTO key_envelopes(workspace_id, user_id, key_id, envelope, created_at)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(workspace_id, user_id, key_id) DO UPDATE SET envelope = excluded.envelope, created_at = excluded.created_at`,
				workspaceID, envelope.UserID, keyID, envelope.Envelope, unixNow(now)); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

// ListWorkspaceMemberDevices returns every device - including revoked ones,
// so a client can still verify the signature on history authenticated
// before a revocation boundary - belonging to any current or former member
// of workspaceID. A client's own signature-verification pass needs this to
// authenticate operations from a fellow workspace member's device, which is
// otherwise information the server never has a reason to reveal outside an
// authorized fellow member (see docs/threat-model.md).
func (s *Storage) ListWorkspaceMemberDevices(ctx context.Context, principal Principal, workspaceID string) ([]Device, error) {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return nil, err
	}
	member, err := s.isActiveMember(ctx, s.db, principal.UserID, workspaceID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.device_id, d.user_id, d.display_name, d.signing_public, d.created_at, d.revoked_at
		FROM devices d JOIN memberships m ON m.user_id = d.user_id
		WHERE m.workspace_id = ? ORDER BY d.user_id, d.device_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Device
	for rows.Next() {
		var item Device
		var created int64
		var revoked sql.NullInt64
		if err := rows.Scan(&item.ID, &item.UserID, &item.DisplayName, &item.PublicKey, &created, &revoked); err != nil {
			return nil, err
		}
		item.CreatedAt = time.Unix(created, 0).UTC()
		item.RevokedAt = nullableTime(revoked)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Storage) ListDevices(ctx context.Context, principal Principal) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT device_id, user_id, display_name, signing_public, created_at, revoked_at
		FROM devices WHERE user_id = ? ORDER BY created_at, device_id`, principal.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Device
	for rows.Next() {
		var item Device
		var created int64
		var revoked sql.NullInt64
		if err := rows.Scan(&item.ID, &item.UserID, &item.DisplayName, &item.PublicKey, &created, &revoked); err != nil {
			return nil, err
		}
		item.CreatedAt = time.Unix(created, 0).UTC()
		item.RevokedAt = nullableTime(revoked)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Storage) RevokeDevice(ctx context.Context, principal Principal, deviceID string, now time.Time) error {
	if err := validateID(deviceID, "device_id"); err != nil {
		return err
	}
	_, err := withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		result, err := transaction.ExecContext(ctx, `
			UPDATE devices SET revoked_at = ?
			WHERE device_id = ? AND user_id = ? AND revoked_at IS NULL`, unixNow(now), deviceID, principal.UserID)
		if err != nil {
			return struct{}{}, err
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return struct{}{}, ErrNotFound
		}
		if _, err := transaction.ExecContext(ctx, `DELETE FROM sessions WHERE device_id = ?`, deviceID); err != nil {
			return struct{}{}, err
		}
		if _, err := transaction.ExecContext(ctx, `DELETE FROM challenges WHERE device_id = ?`, deviceID); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Storage) AddDevice(ctx context.Context, principal Principal, deviceID, displayName string, publicKey []byte, now time.Time) error {
	if err := validateID(deviceID, "device_id"); err != nil {
		return err
	}
	if displayName == "" || len(displayName) > 200 || len(publicKey) != 32 || allZero(publicKey) {
		return fmt.Errorf("%w: invalid device registration", ErrInvalid)
	}
	_, err := withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		var activeDevices int
		if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM devices WHERE user_id = ? AND revoked_at IS NULL`, principal.UserID).
			Scan(&activeDevices); err != nil {
			return struct{}{}, err
		}
		if activeDevices >= 3 {
			return struct{}{}, fmt.Errorf("%w: a user may have at most three active devices", ErrConflict)
		}
		_, err := transaction.ExecContext(ctx, `
			INSERT INTO devices(device_id, user_id, display_name, signing_public, created_at)
			VALUES (?, ?, ?, ?, ?)`, deviceID, principal.UserID, displayName, publicKey, unixNow(now))
		return struct{}{}, classifyConstraint(err)
	})
	return err
}

func (s *Storage) RevokeMember(ctx context.Context, principal Principal, workspaceID, userID string, now time.Time) error {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return err
	}
	if err := validateID(userID, "user_id"); err != nil {
		return err
	}
	_, err := withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		if err := requireWorkspaceOwner(ctx, transaction, principal.UserID, workspaceID); err != nil {
			return struct{}{}, err
		}
		result, err := transaction.ExecContext(ctx, `
			UPDATE memberships SET revoked_at = ?
			WHERE workspace_id = ? AND user_id = ? AND role != 'owner' AND revoked_at IS NULL`,
			unixNow(now), workspaceID, userID)
		if err != nil {
			return struct{}{}, err
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return struct{}{}, ErrNotFound
		}
		return struct{}{}, nil
	})
	return err
}

func requireWorkspaceOwner(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID, workspaceID string) error {
	var role string
	err := query.QueryRowContext(ctx, `
		SELECT role FROM memberships WHERE workspace_id = ? AND user_id = ? AND revoked_at IS NULL`,
		workspaceID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if role != "owner" {
		return ErrForbidden
	}
	return nil
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.Unix(value.Int64, 0).UTC()
	return &result
}

package server

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const authSignatureDomain = "beresta.auth.v1"

type Invite struct {
	ID          string    `json:"invite_id"`
	Code        string    `json:"code,omitempty"`
	DisplayName string    `json:"display_name"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type Registration struct {
	InviteCode        string `json:"invite_code"`
	UserID            string `json:"user_id"`
	IdentityPublic    []byte `json:"identity_public"`
	AuthorityPublic   []byte `json:"authority_public"`
	DeviceID          string `json:"device_id"`
	DeviceName        string `json:"device_name"`
	SigningPublic     []byte `json:"signing_public"`
	WorkspaceID       string `json:"workspace_id"`
	WorkspaceKeyID    string `json:"workspace_key_id"`
	WorkspaceEnvelope []byte `json:"workspace_envelope"`
	KeybagCiphertext  []byte `json:"keybag_ciphertext"`
}

type RegistrationResult struct {
	UserID      string `json:"user_id"`
	DeviceID    string `json:"device_id"`
	WorkspaceID string `json:"workspace_id"`
}

type Challenge struct {
	ID                string    `json:"challenge_id"`
	DeviceID          string    `json:"device_id"`
	ServerFingerprint string    `json:"server_fingerprint"`
	Nonce             []byte    `json:"nonce"`
	Scope             string    `json:"scope"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type ChallengeProof struct {
	ChallengeID       string `json:"challenge_id"`
	DeviceID          string `json:"device_id"`
	ServerFingerprint string `json:"server_fingerprint"`
	Nonce             []byte `json:"nonce"`
	Scope             string `json:"scope"`
	Signature         []byte `json:"signature"`
}

type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	DeviceID  string    `json:"device_id"`
	Scope     string    `json:"scope"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Principal struct {
	UserID    string
	DeviceID  string
	Scope     string
	TokenHash []byte
}

func (s *Storage) CreateInvite(ctx context.Context, displayName string, ttl time.Duration, now time.Time) (Invite, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len(displayName) > 200 || ttl <= 0 || ttl > 30*24*time.Hour {
		return Invite{}, fmt.Errorf("%w: invalid invite name or lifetime", ErrInvalid)
	}
	id, err := newID()
	if err != nil {
		return Invite{}, err
	}
	code, digest, err := randomToken(32)
	if err != nil {
		return Invite{}, err
	}
	expires := now.UTC().Add(ttl)
	_, err = withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		_, err := transaction.ExecContext(ctx, `
			INSERT INTO invites(invite_id, code_hash, display_name, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?)`, id, digest, displayName, unixNow(expires), unixNow(now))
		return struct{}{}, err
	})
	if err != nil {
		return Invite{}, err
	}
	return Invite{ID: id, Code: code, DisplayName: displayName, ExpiresAt: expires}, nil
}

func (s *Storage) Register(ctx context.Context, request Registration, now time.Time) (RegistrationResult, error) {
	if err := validateRegistration(request); err != nil {
		return RegistrationResult{}, err
	}
	if int64(len(request.WorkspaceEnvelope)) > s.config.Limits.MaxOperationBytes ||
		int64(len(request.KeybagCiphertext)) > s.config.Limits.MaxOperationBytes*4 {
		return RegistrationResult{}, fmt.Errorf("%w: registration ciphertext exceeds configured limits", ErrInvalid)
	}
	digest := sha256.Sum256([]byte(request.InviteCode))
	return withWriteTx(ctx, s, func(transaction *sql.Tx) (RegistrationResult, error) {
		var userCount int
		if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&userCount); err != nil {
			return RegistrationResult{}, err
		}
		if userCount >= 5 {
			return RegistrationResult{}, fmt.Errorf("%w: the supported five-user limit has been reached", ErrConflict)
		}
		var inviteID, displayName string
		err := transaction.QueryRowContext(ctx, `
			SELECT invite_id, display_name FROM invites
			WHERE code_hash = ? AND consumed_at IS NULL AND expires_at > ?`, digest[:], unixNow(now),
		).Scan(&inviteID, &displayName)
		if errors.Is(err, sql.ErrNoRows) {
			return RegistrationResult{}, ErrUnauthorized
		}
		if err != nil {
			return RegistrationResult{}, err
		}
		result, err := transaction.ExecContext(ctx, `
			UPDATE invites SET consumed_at = ? WHERE invite_id = ? AND consumed_at IS NULL`, unixNow(now), inviteID)
		if err != nil {
			return RegistrationResult{}, err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return RegistrationResult{}, ErrUnauthorized
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO users(user_id, display_name, identity_public, authority_public, quota_bytes, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, request.UserID, displayName, request.IdentityPublic,
			request.AuthorityPublic, s.config.Limits.UserQuotaBytes, unixNow(now)); err != nil {
			return RegistrationResult{}, classifyConstraint(err)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO devices(device_id, user_id, display_name, signing_public, created_at)
			VALUES (?, ?, ?, ?, ?)`, request.DeviceID, request.UserID, request.DeviceName,
			request.SigningPublic, unixNow(now)); err != nil {
			return RegistrationResult{}, classifyConstraint(err)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO keybags(user_id, version, ciphertext, updated_at) VALUES (?, 1, ?, ?)`,
			request.UserID, request.KeybagCiphertext, unixNow(now)); err != nil {
			return RegistrationResult{}, err
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO workspaces(workspace_id, owner_user_id, current_key_id, created_at)
			VALUES (?, ?, ?, ?)`, request.WorkspaceID, request.UserID, request.WorkspaceKeyID, unixNow(now)); err != nil {
			return RegistrationResult{}, classifyConstraint(err)
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO memberships(workspace_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
			request.WorkspaceID, request.UserID, unixNow(now)); err != nil {
			return RegistrationResult{}, err
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO key_envelopes(workspace_id, user_id, key_id, envelope, created_at)
			VALUES (?, ?, ?, ?, ?)`, request.WorkspaceID, request.UserID, request.WorkspaceKeyID,
			request.WorkspaceEnvelope, unixNow(now)); err != nil {
			return RegistrationResult{}, err
		}
		return RegistrationResult{UserID: request.UserID, DeviceID: request.DeviceID, WorkspaceID: request.WorkspaceID}, nil
	})
}

func validateRegistration(request Registration) error {
	for field, value := range map[string]string{
		"user_id": request.UserID, "device_id": request.DeviceID, "workspace_id": request.WorkspaceID,
	} {
		if err := validateID(value, field); err != nil {
			return err
		}
	}
	if err := validateOpaqueID(request.WorkspaceKeyID, "workspace_key_id"); err != nil {
		return err
	}
	if len(request.InviteCode) < 40 || len(request.InviteCode) > 128 || len(request.DeviceName) == 0 || len(request.DeviceName) > 200 ||
		len(request.IdentityPublic) != 32 || len(request.AuthorityPublic) != 32 || len(request.SigningPublic) != ed25519.PublicKeySize ||
		allZero(request.IdentityPublic) || allZero(request.AuthorityPublic) || allZero(request.SigningPublic) ||
		len(request.WorkspaceEnvelope) == 0 || len(request.KeybagCiphertext) == 0 {
		return fmt.Errorf("%w: registration fields have invalid sizes", ErrInvalid)
	}
	return nil
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

func (s *Storage) IssueChallenge(ctx context.Context, deviceID, scope string, now time.Time) (Challenge, error) {
	if err := validateID(deviceID, "device_id"); err != nil {
		return Challenge{}, err
	}
	if !validScope(scope) {
		return Challenge{}, fmt.Errorf("%w: invalid authentication scope", ErrInvalid)
	}
	id, err := newID()
	if err != nil {
		return Challenge{}, err
	}
	nonceToken, digest, err := randomToken(32)
	if err != nil {
		return Challenge{}, err
	}
	nonce := []byte(nonceToken)
	expires := now.UTC().Add(s.config.Auth.ChallengeTTL.Value())
	_, err = withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		var active int
		if err := transaction.QueryRowContext(ctx, `SELECT 1 FROM devices WHERE device_id = ? AND revoked_at IS NULL`, deviceID).Scan(&active); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return struct{}{}, ErrUnauthorized
			}
			return struct{}{}, err
		}
		_, err := transaction.ExecContext(ctx, `
			INSERT INTO challenges(challenge_id, device_id, nonce_hash, scope, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, id, deviceID, digest, scope, unixNow(expires), unixNow(now))
		return struct{}{}, err
	})
	if err != nil {
		return Challenge{}, err
	}
	return Challenge{ID: id, DeviceID: deviceID, ServerFingerprint: s.serverFingerprint, Nonce: nonce, Scope: scope, ExpiresAt: expires}, nil
}

func (s *Storage) VerifyChallenge(ctx context.Context, proof ChallengeProof, now time.Time) (Session, error) {
	return s.verifyChallenge(ctx, proof, nil, now)
}

// RefreshSession consumes a fresh device proof and replaces the authenticated
// session in the same transaction, so a failed refresh cannot leave two live
// sessions or invalidate the old session without issuing its replacement.
func (s *Storage) RefreshSession(ctx context.Context, principal Principal, proof ChallengeProof, now time.Time) (Session, error) {
	if proof.DeviceID != principal.DeviceID || !scopeAllows(principal.Scope, proof.Scope) {
		return Session{}, ErrForbidden
	}
	return s.verifyChallenge(ctx, proof, principal.TokenHash, now)
}

func (s *Storage) verifyChallenge(ctx context.Context, proof ChallengeProof, replacedTokenHash []byte, now time.Time) (Session, error) {
	if err := validateID(proof.ChallengeID, "challenge_id"); err != nil {
		return Session{}, err
	}
	if err := validateID(proof.DeviceID, "device_id"); err != nil {
		return Session{}, err
	}
	if proof.ServerFingerprint != s.serverFingerprint || len(proof.Nonce) == 0 || len(proof.Signature) != ed25519.SignatureSize || !validScope(proof.Scope) {
		return Session{}, ErrUnauthorized
	}
	digest := sha256.Sum256(proof.Nonce)
	message := authSignatureInput(proof)
	token, tokenHash, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	expires := now.UTC().Add(s.config.Auth.SessionTTL.Value())
	return withWriteTx(ctx, s, func(transaction *sql.Tx) (Session, error) {
		var signingPublic []byte
		var userID string
		err := transaction.QueryRowContext(ctx, `
			SELECT d.signing_public, d.user_id
			FROM challenges c JOIN devices d ON d.device_id = c.device_id
			WHERE c.challenge_id = ? AND c.device_id = ? AND c.nonce_hash = ? AND c.scope = ?
			  AND c.consumed_at IS NULL AND c.expires_at > ? AND d.revoked_at IS NULL`,
			proof.ChallengeID, proof.DeviceID, digest[:], proof.Scope, unixNow(now),
		).Scan(&signingPublic, &userID)
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrUnauthorized
		}
		if err != nil {
			return Session{}, err
		}
		if !ed25519.Verify(ed25519.PublicKey(signingPublic), message, proof.Signature) {
			return Session{}, ErrUnauthorized
		}
		result, err := transaction.ExecContext(ctx, `
			UPDATE challenges SET consumed_at = ? WHERE challenge_id = ? AND consumed_at IS NULL`, unixNow(now), proof.ChallengeID)
		if err != nil {
			return Session{}, err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return Session{}, ErrUnauthorized
		}
		if len(replacedTokenHash) != 0 {
			result, err := transaction.ExecContext(ctx, `
				DELETE FROM sessions WHERE token_hash = ? AND user_id = ? AND device_id = ?`,
				replacedTokenHash, userID, proof.DeviceID)
			if err != nil {
				return Session{}, err
			}
			if rows, _ := result.RowsAffected(); rows != 1 {
				return Session{}, ErrUnauthorized
			}
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO sessions(token_hash, user_id, device_id, scope, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, tokenHash, userID, proof.DeviceID, proof.Scope,
			unixNow(expires), unixNow(now)); err != nil {
			return Session{}, err
		}
		return Session{Token: token, UserID: userID, DeviceID: proof.DeviceID, Scope: proof.Scope, ExpiresAt: expires}, nil
	})
}

func (s *Storage) Authenticate(ctx context.Context, token string, now time.Time) (Principal, error) {
	if len(token) < 40 {
		return Principal{}, ErrUnauthorized
	}
	digest := sha256.Sum256([]byte(token))
	var principal Principal
	err := s.db.QueryRowContext(ctx, `
		SELECT s.user_id, s.device_id, s.scope
		FROM sessions s JOIN devices d ON d.device_id = s.device_id
		WHERE s.token_hash = ? AND s.expires_at > ? AND d.revoked_at IS NULL`, digest[:], unixNow(now),
	).Scan(&principal.UserID, &principal.DeviceID, &principal.Scope)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, err
	}
	principal.TokenHash = digest[:]
	return principal, nil
}

func (s *Storage) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		_, err := transaction.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
		return struct{}{}, err
	})
	return err
}

func authSignatureInput(proof ChallengeProof) []byte {
	fields := [][]byte{
		[]byte(authSignatureDomain), []byte(proof.ChallengeID), []byte(proof.DeviceID), []byte(proof.ServerFingerprint), []byte(proof.Scope), proof.Nonce,
	}
	var result []byte
	for _, field := range fields {
		length := uint32(len(field))
		result = append(result, byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
		result = append(result, field...)
	}
	return result
}

func validScope(scope string) bool {
	return scope == "sync"
}

func scopeAllows(actual, required string) bool {
	return actual == required
}

func classifyConstraint(err error) error {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "constraint") {
		return fmt.Errorf("%w: resource already exists or violates a constraint", ErrConflict)
	}
	return err
}

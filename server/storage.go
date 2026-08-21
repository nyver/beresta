package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

var canonicalIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var (
	ErrNotFound         = errors.New("not found")
	ErrForbidden        = errors.New("forbidden")
	ErrConflict         = errors.New("conflict")
	ErrInvalid          = errors.New("invalid input")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrQuota            = errors.New("quota exceeded")
	ErrRateLimited      = errors.New("rate limited")
	ErrSnapshotRequired = errors.New("snapshot required")
)

// Storage owns all server-side opaque persistence. The single database
// connection and this mutex make every write transaction explicitly serialized.
type Storage struct {
	db                *sql.DB
	dataRoot          string
	config            Config
	serverFingerprint string
	writeMu           sync.Mutex
}

func NewStorage(database *sql.DB, dataRoot string, cfg Config, serverFingerprint string) *Storage {
	return &Storage{db: database, dataRoot: dataRoot, config: cfg, serverFingerprint: serverFingerprint}
}

func (s *Storage) applyConfiguredQuotas(ctx context.Context) error {
	_, err := withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		_, err := transaction.ExecContext(ctx, `UPDATE users SET quota_bytes = ?`, s.config.Limits.UserQuotaBytes)
		return struct{}{}, err
	})
	return err
}

func validateID(value, field string) error {
	if !canonicalIDPattern.MatchString(value) {
		return fmt.Errorf("%w: %s must be a canonical UUID", ErrInvalid, field)
	}
	return nil
}

func validateOpaqueID(value, field string) error {
	if len(value) != 32 {
		return fmt.Errorf("%w: %s must contain 32 lowercase hexadecimal bytes", ErrInvalid, field)
	}
	if decoded, err := hex.DecodeString(value); err != nil || len(decoded) != 16 || value != strings.ToLower(value) {
		return fmt.Errorf("%w: %s must contain 16 lowercase hexadecimal bytes", ErrInvalid, field)
	}
	return nil
}

func decodeCanonicalID(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil {
		return nil, err
	}
	id, err := model.ParseID(decoded)
	if err != nil {
		return nil, err
	}
	return id.Bytes(), nil
}

func decodeOpaqueID(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 16 {
		return nil, ErrInvalid
	}
	return decoded, nil
}

func randomToken(byteCount int) (string, []byte, error) {
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", nil, fmt.Errorf("generate random token: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(value)
	digest := sha256.Sum256([]byte(encoded))
	return encoded, digest[:], nil
}

func newID() (string, error) {
	id, err := model.NewID()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func withWriteTx[T any](ctx context.Context, storage *Storage, fn func(*sql.Tx) (T, error)) (T, error) {
	storage.writeMu.Lock()
	defer storage.writeMu.Unlock()
	var zero T
	transaction, err := storage.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	result, err := fn(transaction)
	if err != nil {
		transaction.Rollback()
		return zero, err
	}
	if err := transaction.Commit(); err != nil {
		return zero, err
	}
	return result, nil
}

func (s *Storage) isActiveMember(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID, workspaceID string) (bool, error) {
	var one int
	err := query.QueryRowContext(ctx, `
		SELECT 1 FROM memberships
		WHERE workspace_id = ? AND user_id = ? AND revoked_at IS NULL`, workspaceID, userID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func unixNow(now time.Time) int64 { return now.UTC().Unix() }

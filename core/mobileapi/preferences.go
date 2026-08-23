package mobileapi

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

const (
	retentionAll      = "all"
	retentionSelected = "selected_notebooks"
	retentionMetadata = "metadata_only"
	maxCacheBytes     = int64(20 << 30)
)

type mobilePreferences struct {
	Language            string   `json:"language"`
	AutoLockMinutes     int      `json:"auto_lock_minutes"`
	BackupDestination   string   `json:"backup_destination"`
	AttachmentRetention string   `json:"attachment_retention"`
	SelectedNotebooks   []string `json:"selected_notebooks"`
	CacheLimitBytes     int64    `json:"cache_limit_bytes"`
}

func defaultMobilePreferences() mobilePreferences {
	return mobilePreferences{
		Language:            "en",
		AutoLockMinutes:     5,
		AttachmentRetention: retentionAll,
		SelectedNotebooks:   []string{},
		CacheLimitBytes:     512 << 20,
	}
}

func (p mobilePreferences) validate() error {
	if p.Language != "en" && p.Language != "ru" {
		return errors.New("mobileapi: unsupported language")
	}
	if p.AutoLockMinutes < 0 || p.AutoLockMinutes > 24*60 || p.CacheLimitBytes < 0 || p.CacheLimitBytes > maxCacheBytes {
		return errors.New("mobileapi: invalid mobile resource policy")
	}
	if p.AttachmentRetention != retentionAll && p.AttachmentRetention != retentionSelected && p.AttachmentRetention != retentionMetadata {
		return errors.New("mobileapi: invalid attachment retention mode")
	}
	seen := make(map[string]struct{}, len(p.SelectedNotebooks))
	for _, raw := range p.SelectedNotebooks {
		if _, err := parseID(raw); err != nil {
			return err
		}
		if _, duplicate := seen[raw]; duplicate {
			return errors.New("mobileapi: duplicate selected notebook")
		}
		seen[raw] = struct{}{}
	}
	if p.AttachmentRetention != retentionSelected && len(p.SelectedNotebooks) != 0 {
		return errors.New("mobileapi: selected notebooks require selected retention mode")
	}
	return nil
}

func (s *Service) GetSettings(requestID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	prefs, err := loadMobilePreferences(ctx, value.DB())
	if err != nil {
		return "", err
	}
	return marshal(prefs)
}

func (s *Service) UpdateSettings(requestID, encoded string) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	var prefs mobilePreferences
	if err := strictJSON(encoded, &prefs); err != nil {
		return err
	}
	if err := prefs.validate(); err != nil {
		return err
	}
	value, _, err := s.accountState()
	if err != nil {
		return err
	}
	contents, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	_, err = value.DB().ExecContext(ctx, `INSERT INTO mobile_preferences(singleton, value_json, updated_unix_ms) VALUES (1, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET value_json = excluded.value_json, updated_unix_ms = excluded.updated_unix_ms`, contents, time.Now().UnixMilli())
	if err == nil {
		s.emit("settings_changed", prefs)
	}
	return err
}

func loadMobilePreferences(ctx context.Context, db *sql.DB) (mobilePreferences, error) {
	prefs := defaultMobilePreferences()
	var contents []byte
	err := db.QueryRowContext(ctx, `SELECT value_json FROM mobile_preferences WHERE singleton = 1`).Scan(&contents)
	if errors.Is(err, sql.ErrNoRows) {
		return prefs, nil
	}
	if err != nil {
		return prefs, err
	}
	if err := json.Unmarshal(contents, &prefs); err != nil {
		return prefs, errors.New("mobileapi: stored preferences are malformed")
	}
	if prefs.SelectedNotebooks == nil {
		// A nil slice marshals to JSON null, which the Flutter client cannot
		// cast to a List; a row persisted before this normalization existed
		// can still carry that null.
		prefs.SelectedNotebooks = []string{}
	}
	return prefs, prefs.validate()
}

// RecordCachedAttachment records only redundant downloaded ciphertext. Local
// unsynchronized originals must pass synchronizedOriginal=false and are never
// returned by PlanCacheEviction.
func (s *Service) RecordCachedAttachment(requestID, blobIDHex, notebookID string, sizeBytes int64, pinned, synchronizedOriginal bool) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	blobID, err := hex.DecodeString(blobIDHex)
	if err != nil || len(blobID) != 32 || hex.EncodeToString(blobID) != blobIDHex || sizeBytes <= 0 {
		return errors.New("mobileapi: invalid attachment cache record")
	}
	notebook, err := optionalID(notebookID)
	if err != nil {
		return err
	}
	value, _, err := s.accountState()
	if err != nil {
		return err
	}
	_, err = value.DB().ExecContext(ctx, `INSERT INTO mobile_attachment_cache(blob_id, notebook_id, size_bytes, pinned, synchronized_original, last_access_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(blob_id) DO UPDATE SET notebook_id = excluded.notebook_id, size_bytes = excluded.size_bytes,
		pinned = excluded.pinned, synchronized_original = excluded.synchronized_original, last_access_unix_ms = excluded.last_access_unix_ms`,
		blobID, nullableID(notebook), sizeBytes, pinned, synchronizedOriginal, time.Now().UnixMilli())
	return err
}

// PlanCacheEviction removes cache bookkeeping atomically and returns the
// redundant encrypted files the Android host may delete. Pinned items and
// unsynchronized originals are never candidates.
func (s *Service) PlanCacheEviction(requestID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	prefs, err := loadMobilePreferences(ctx, value.DB())
	if err != nil {
		return "", err
	}
	tx, err := value.DB().BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM mobile_attachment_cache`).Scan(&total); err != nil {
		return "", err
	}
	selected := make(map[string]struct{}, len(prefs.SelectedNotebooks))
	for _, id := range prefs.SelectedNotebooks {
		parsed, parseErr := parseID(id)
		if parseErr != nil {
			return "", parseErr
		}
		selected[hex.EncodeToString(parsed.Bytes())] = struct{}{}
	}
	rows, err := tx.QueryContext(ctx, `SELECT blob_id, notebook_id, size_bytes, pinned, synchronized_original
		FROM mobile_attachment_cache ORDER BY last_access_unix_ms, blob_id`)
	if err != nil {
		return "", err
	}
	var evict [][]byte
	for rows.Next() {
		var id []byte
		var notebookID []byte
		var size int64
		var pinned, synchronizedOriginal bool
		if err := rows.Scan(&id, &notebookID, &size, &pinned, &synchronizedOriginal); err != nil {
			rows.Close()
			return "", err
		}
		if !shouldEvictCachedAttachment(prefs, selected, notebookID, pinned, synchronizedOriginal, total) {
			continue
		}
		evict = append(evict, id)
		total -= size
	}
	err = rows.Close()
	if err != nil {
		return "", err
	}
	result := make([]string, len(evict))
	for i, id := range evict {
		result[i] = hex.EncodeToString(id)
		if _, err := tx.ExecContext(ctx, `DELETE FROM mobile_attachment_cache WHERE blob_id = ?`, id); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return marshal(result)
}

func shouldEvictCachedAttachment(prefs mobilePreferences, selected map[string]struct{}, notebookID []byte, pinned, synchronizedOriginal bool, total int64) bool {
	if pinned || !synchronizedOriginal {
		return false
	}
	if prefs.AttachmentRetention == retentionMetadata {
		return true
	}
	if prefs.AttachmentRetention == retentionSelected {
		_, retained := selected[hex.EncodeToString(notebookID)]
		return !retained || total > prefs.CacheLimitBytes
	}
	return total > prefs.CacheLimitBytes
}

func nullableID(id model.ID) any {
	if id.IsZero() {
		return nil
	}
	return id.Bytes()
}

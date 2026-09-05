package mobileapi

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// syncConnectionConfig is the last server connection the user configured on
// this device. It is persisted so the connect dialog can be prefilled after
// it is closed and reopened, and so a previously enabled connection can be
// reattached automatically the next time the account unlocks. Protocol is
// derived from the validated URL instead of being stored separately.
// InviteCode is deliberately excluded: it is a single-use registration token,
// not part of the ongoing connection.
type syncConnectionConfig struct {
	Enabled      bool   `json:"enabled"`
	URL          string `json:"url"`
	Protocol     string `json:"protocol"`
	SecurityMode string `json:"security_mode,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
}

func loadSyncConnectionConfig(ctx context.Context, db *sql.DB) (syncConnectionConfig, error) {
	var cfg syncConnectionConfig
	err := db.QueryRowContext(ctx, `SELECT enabled, server_url, security_mode, fingerprint FROM mobile_sync_config WHERE singleton = 1`).
		Scan(&cfg.Enabled, &cfg.URL, &cfg.SecurityMode, &cfg.Fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return syncConnectionConfig{}, nil
	}
	if err == nil && cfg.URL != "" {
		cfg.Protocol = "https"
	}
	return cfg, err
}

func saveSyncConnectionConfig(ctx context.Context, db *sql.DB, cfg syncConnectionConfig) error {
	_, err := db.ExecContext(ctx, `INSERT INTO mobile_sync_config(singleton, enabled, server_url, security_mode, fingerprint, updated_unix_ms)
		VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET enabled = excluded.enabled, server_url = excluded.server_url,
			security_mode = excluded.security_mode, fingerprint = excluded.fingerprint, updated_unix_ms = excluded.updated_unix_ms`,
		cfg.Enabled, cfg.URL, cfg.SecurityMode, cfg.Fingerprint, time.Now().UnixMilli())
	return err
}

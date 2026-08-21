package server

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Runtime owns initialized server resources.
type Runtime struct {
	Config        Config
	DataDirectory string
	Database      *sql.DB
	TLSIdentity   TLSIdentity
	Storage       *Storage
	API           *API
	dataLock      *serverDataLock
}

// Initialize creates a private data root, opens the database, applies embedded
// migrations, and loads or generates the TLS identity.
func Initialize(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	dataDirectory, err := filepath.Abs(cfg.Server.DataDirectory)
	if err != nil {
		return nil, fmt.Errorf("resolve server data directory: %w", err)
	}
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create server data directory: %w", err)
	}
	if err := restrictDirectory(dataDirectory); err != nil {
		return nil, err
	}
	dataLock, err := acquireDataLock(dataDirectory)
	if err != nil {
		return nil, err
	}
	closeDataLock := true
	defer func() {
		if closeDataLock {
			dataLock.Close()
		}
	}()
	for _, directory := range []string{"blobs", "backups"} {
		path := filepath.Join(dataDirectory, directory)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("create server %s directory: %w", directory, err)
		}
		if err := restrictDirectory(path); err != nil {
			return nil, err
		}
	}

	databasePath := filepath.Join(dataDirectory, "beresta.db")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open server database: %w", err)
	}
	database.SetMaxOpenConns(1)
	closeDatabase := true
	defer func() {
		if closeDatabase {
			database.Close()
		}
	}()
	if err := database.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("connect server database: %w", err)
	}
	if err := restrictFile(databasePath); err != nil {
		return nil, err
	}
	if err := configureSQLite(ctx, database, cfg.SQLite); err != nil {
		return nil, err
	}
	if err := applyMigrations(ctx, database); err != nil {
		return nil, err
	}

	identity, err := ensureTLSIdentity(dataDirectory, cfg.TLS, cfg.Server.Listen)
	if err != nil {
		return nil, err
	}
	storage := NewStorage(database, dataDirectory, cfg, identity.Fingerprint)
	if err := storage.applyConfiguredQuotas(ctx); err != nil {
		return nil, fmt.Errorf("apply configured server quotas: %w", err)
	}
	api := NewAPI(storage, cfg)
	closeDatabase = false
	closeDataLock = false
	return &Runtime{
		Config:        cfg,
		DataDirectory: dataDirectory,
		Database:      database,
		TLSIdentity:   identity,
		Storage:       storage,
		API:           api,
		dataLock:      dataLock,
	}, nil
}

func configureSQLite(ctx context.Context, database *sql.DB, cfg SQLiteConfig) error {
	journalMode := "DELETE"
	if cfg.WAL {
		journalMode = "WAL"
	}
	statements := []string{
		"PRAGMA foreign_keys = ON",
		fmt.Sprintf("PRAGMA busy_timeout = %d", cfg.BusyTimeout.Value().Milliseconds()),
		"PRAGMA synchronous = " + strings.ToUpper(cfg.Synchronous),
		"PRAGMA journal_mode = " + journalMode,
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure server SQLite: %w", err)
		}
	}
	var integrity string
	if err := database.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return fmt.Errorf("check server SQLite integrity: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("server SQLite integrity check failed: %s", integrity)
	}
	return nil
}

// Close releases initialized resources.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	var databaseErr error
	if r.Database != nil {
		databaseErr = r.Database.Close()
		r.Database = nil
	}
	var lockErr error
	if r.dataLock != nil {
		lockErr = r.dataLock.Close()
		r.dataLock = nil
	}
	if databaseErr != nil {
		return databaseErr
	}
	return lockErr
}

// Serve starts the TLS 1.3-only versioned API and optional metrics listener.
func (r *Runtime) Serve(ctx context.Context) error {
	if r.Config.Backups.Enabled {
		if _, err := r.Storage.EnsureDailyServerBackup(ctx, time.Now()); err != nil {
			return fmt.Errorf("create startup daily server backup: %w", err)
		}
		go r.runBackupScheduler(ctx)
	}
	go r.runAuthGarbageCollector(ctx)
	pair, err := tls.LoadX509KeyPair(r.TLSIdentity.CertificateFile, r.TLSIdentity.PrivateKeyFile)
	if err != nil {
		return fmt.Errorf("load TLS listener identity: %w", err)
	}
	listener, err := net.Listen("tcp", r.Config.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", r.Config.Server.Listen, err)
	}
	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS13,
	})
	httpServer := &http.Server{
		Handler:           r.API,
		ReadHeaderTimeout: r.Config.Server.ShutdownTimeout.Value(),
		ReadTimeout:       35 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	servers := []*http.Server{httpServer}
	listeners := []net.Listener{tlsListener}
	if r.Config.Metrics.Enabled {
		metricsListener, err := net.Listen("tcp", r.Config.Metrics.Listen)
		if err != nil {
			listener.Close()
			return fmt.Errorf("listen for metrics on %s: %w", r.Config.Metrics.Listen, err)
		}
		metricsServer := &http.Server{
			Handler:           http.HandlerFunc(r.API.metricsHandler),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       30 * time.Second,
			MaxHeaderBytes:    8 << 10,
		}
		servers = append(servers, metricsServer)
		listeners = append(listeners, metricsListener)
	}

	serveErrors := make(chan error, len(servers))
	go func() {
		serveErrors <- httpServer.Serve(tlsListener)
	}()
	if len(servers) == 2 {
		go func() {
			serveErrors <- servers[1].Serve(listeners[1])
		}()
	}
	select {
	case err := <-serveErrors:
		shutdownContext, cancel := context.WithTimeout(context.Background(), r.Config.Server.ShutdownTimeout.Value())
		defer cancel()
		for _, server := range servers {
			server.Shutdown(shutdownContext)
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTPS: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), r.Config.Server.ShutdownTimeout.Value())
		defer cancel()
		for _, server := range servers {
			if err := server.Shutdown(shutdownContext); err != nil {
				return fmt.Errorf("shut down server: %w", err)
			}
		}
		for range servers {
			if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("serve during shutdown: %w", err)
			}
		}
		return nil
	}
}

// runAuthGarbageCollector deletes expired challenges and sessions. Both tables
// are append-only during normal operation — a row is otherwise removed only
// when its device is revoked — so without this sweep every login and every
// (unauthenticated) challenge request grows the database permanently.
func (r *Runtime) runAuthGarbageCollector(ctx context.Context) {
	ticker := time.NewTicker(authGarbageCollectionInterval)
	defer ticker.Stop()
	for {
		if err := r.Storage.PurgeExpiredAuthRecords(ctx, time.Now()); err != nil {
			slog.Warn("expired authentication record sweep failed", "error_class", "auth_gc_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) runBackupScheduler(ctx context.Context) {
	for {
		now := time.Now()
		scheduled, err := time.ParseInLocation("15:04", r.Config.Backups.DailyAt, now.Location())
		if err != nil {
			slog.Error("daily backup scheduler stopped", "error_class", "invalid_schedule")
			return
		}
		next := time.Date(now.Year(), now.Month(), now.Day(), scheduled.Hour(), scheduled.Minute(), 0, 0, now.Location())
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if _, err := r.Storage.EnsureDailyServerBackup(ctx, time.Now()); err != nil {
				slog.Error("daily server backup failed", "error_class", "backup_failed")
			}
		}
	}
}

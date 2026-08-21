package server

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"gopkg.in/yaml.v3"
)

const (
	defaultDataDirectory = "./data"
	defaultListenAddress = "127.0.0.1:8443"

	// BlobChunkBytes is the attachment chunk size mandated by the sync
	// protocol. It must stay equal to core/transport.BlobChunkBytes; the two
	// are separate constants only to keep the server free of a client
	// dependency.
	BlobChunkBytes = 4 << 20

	// MinKeepDailyBackups and MaxKeepDailyBackups bound backups.keep_daily.
	MinKeepDailyBackups = 1
	MaxKeepDailyBackups = 365
)

// Duration is a YAML duration with the same syntax as time.ParseDuration.
type Duration time.Duration

func duration(value time.Duration) Duration {
	return Duration(value)
}

// Value returns the underlying duration.
func (d Duration) Value() time.Duration {
	return time.Duration(d)
}

// UnmarshalText rejects empty and non-positive durations.
func (d *Duration) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("parse duration: %w", err)
	}
	if value <= 0 {
		return errors.New("duration must be positive")
	}
	*d = Duration(value)
	return nil
}

// Config contains the complete server configuration. Fields not yet consumed
// by a phase are still decoded strictly so configuration mistakes fail early.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	TLS     TLSConfig     `yaml:"tls"`
	Auth    AuthConfig    `yaml:"auth"`
	Limits  LimitsConfig  `yaml:"limits"`
	SQLite  SQLiteConfig  `yaml:"sqlite"`
	Backups BackupsConfig `yaml:"backups"`
	Logging LoggingConfig `yaml:"logging"`
	Metrics MetricsConfig `yaml:"metrics"`
}

type ServerConfig struct {
	Listen          string   `yaml:"listen"`
	DataDirectory   string   `yaml:"data_dir"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
}

type TLSConfig struct {
	Mode            string     `yaml:"mode"`
	CertificateFile string     `yaml:"certificate_file"`
	PrivateKeyFile  string     `yaml:"private_key_file"`
	ACME            ACMEConfig `yaml:"acme"`
}

type ACMEConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Hostname string `yaml:"hostname"`
	Email    string `yaml:"email"`
	CacheDir string `yaml:"cache_dir"`
}

type AuthConfig struct {
	ChallengeTTL Duration `yaml:"challenge_ttl"`
	SessionTTL   Duration `yaml:"session_ttl"`
	InviteTTL    Duration `yaml:"invite_ttl"`
}

type LimitsConfig struct {
	MaxOperationBytes int64 `yaml:"max_operation_bytes"`
	MaxBlobBytes      int64 `yaml:"max_blob_bytes"`
	// BlobChunkBytes is fixed by the sync protocol and is not configurable:
	// clients chunk uploads against the same constant (core/transport.
	// BlobChunkBytes), so a server-side change would corrupt transfers rather
	// than tune them. It is populated from BlobChunkBytes for the call sites
	// that read it through the config.
	BlobChunkBytes        int64    `yaml:"-"`
	UserQuotaBytes        int64    `yaml:"user_quota_bytes"`
	MaxOperationsPerBatch int      `yaml:"max_operations_per_batch"`
	MaxHLCFutureSkew      Duration `yaml:"max_hlc_future_skew"`
	MaxHLCPastAge         Duration `yaml:"max_hlc_past_age"`
	RequestsPerSecond     int      `yaml:"requests_per_second"`
	RequestBurst          int      `yaml:"request_burst"`
}

type SQLiteConfig struct {
	BusyTimeout Duration `yaml:"busy_timeout"`
	Synchronous string   `yaml:"synchronous"`
	WAL         bool     `yaml:"wal"`
}

type BackupsConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Directory string `yaml:"directory"`
	KeepDaily int    `yaml:"keep_daily"`
	DailyAt   string `yaml:"daily_at"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

// DefaultConfig returns server defaults suitable for a local-only bind.
func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Listen:          defaultListenAddress,
			DataDirectory:   defaultDataDirectory,
			ShutdownTimeout: duration(15 * time.Second),
		},
		TLS: TLSConfig{
			Mode: "self_signed",
			ACME: ACMEConfig{CacheDir: "./data/acme"},
		},
		Auth: AuthConfig{
			ChallengeTTL: duration(5 * time.Minute),
			SessionTTL:   duration(24 * time.Hour),
			InviteTTL:    duration(24 * time.Hour),
		},
		Limits: LimitsConfig{
			MaxOperationBytes:     1 << 20,
			MaxBlobBytes:          2 << 30,
			BlobChunkBytes:        BlobChunkBytes,
			UserQuotaBytes:        20 << 30,
			MaxOperationsPerBatch: 1000,
			MaxHLCFutureSkew:      duration(5 * time.Minute),
			MaxHLCPastAge:         duration(365 * 24 * time.Hour),
			RequestsPerSecond:     20,
			RequestBurst:          40,
		},
		SQLite: SQLiteConfig{
			BusyTimeout: duration(5 * time.Second),
			Synchronous: "NORMAL",
			WAL:         true,
		},
		Backups: BackupsConfig{
			Enabled:   true,
			Directory: "./data/backups",
			KeepDaily: 7,
			DailyAt:   "02:00",
		},
		Logging: LoggingConfig{Level: "info", Format: "json"},
		Metrics: MetricsConfig{Listen: "127.0.0.1:9090"},
	}
}

// LoadConfig overlays an optional YAML file on defaults. dataOverride, when
// non-empty, always wins over server.data_dir.
func LoadConfig(path, dataOverride string) (Config, error) {
	cfg := DefaultConfig()
	file, err := os.Open(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("open server configuration: %w", err)
		}
	} else {
		defer file.Close()
		decoder := yaml.NewDecoder(file)
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("decode server configuration: %w", err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			if err == nil {
				return Config{}, errors.New("decode server configuration: multiple YAML documents are not allowed")
			}
			return Config{}, fmt.Errorf("decode server configuration: %w", err)
		}
	}

	if dataOverride != "" {
		if cfg.Backups.Directory == "./data/backups" {
			cfg.Backups.Directory = filepath.Join(dataOverride, "backups")
		}
		if cfg.TLS.ACME.CacheDir == "./data/acme" {
			cfg.TLS.ACME.CacheDir = filepath.Join(dataOverride, "acme")
		}
		cfg.Server.DataDirectory = dataOverride
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks security and lifecycle invariants used by the server.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Server.DataDirectory) == "" {
		return errors.New("server.data_dir must not be empty")
	}
	if strings.TrimSpace(c.Server.Listen) == "" {
		return errors.New("server.listen must not be empty")
	}
	if _, _, err := net.SplitHostPort(c.Server.Listen); err != nil {
		return fmt.Errorf("server.listen must be a host:port address: %w", err)
	}
	if c.Server.ShutdownTimeout.Value() <= 0 {
		return errors.New("server.shutdown_timeout must be positive")
	}
	if c.Auth.SessionTTL.Value() <= 0 || c.Auth.SessionTTL.Value() > 24*time.Hour {
		return errors.New("auth.session_ttl must be positive and no greater than 24h")
	}
	if c.Auth.ChallengeTTL.Value() <= 0 || c.Auth.ChallengeTTL.Value() > 15*time.Minute {
		return errors.New("auth.challenge_ttl must be positive and no greater than 15m")
	}
	if c.Auth.InviteTTL.Value() <= 0 || c.Auth.InviteTTL.Value() > 30*24*time.Hour {
		return errors.New("auth.invite_ttl must be positive and no greater than 720h")
	}
	if c.TLS.ACME.Enabled {
		return errors.New("tls.acme.enabled is not available in this phase")
	}
	switch c.TLS.Mode {
	case "self_signed":
		if c.TLS.CertificateFile != "" || c.TLS.PrivateKeyFile != "" {
			return errors.New("tls certificate paths must be empty in self_signed mode")
		}
	case "files":
		if c.TLS.CertificateFile == "" || c.TLS.PrivateKeyFile == "" {
			return errors.New("tls.certificate_file and tls.private_key_file are required in files mode")
		}
	default:
		return fmt.Errorf("unsupported tls.mode %q", c.TLS.Mode)
	}
	// Not operator-settable (see LimitsConfig.BlobChunkBytes); checked so a
	// directly constructed Config cannot reach the blob handlers with a zero
	// chunk size.
	if c.Limits.BlobChunkBytes != BlobChunkBytes {
		return fmt.Errorf("limits.blob_chunk_bytes is protocol-fixed at %d", BlobChunkBytes)
	}
	if c.Limits.MaxOperationBytes <= 0 || c.Limits.MaxOperationBytes > corecrypto.MaxOperationCiphertextBytes ||
		c.Limits.MaxBlobBytes <= 0 || c.Limits.UserQuotaBytes <= 0 {
		return errors.New("operation, blob, and user quota byte limits are outside supported bounds")
	}
	if c.Limits.MaxHLCFutureSkew.Value() <= 0 {
		return errors.New("limits.max_hlc_future_skew must be positive")
	}
	if c.Limits.MaxHLCPastAge.Value() <= 0 {
		return errors.New("limits.max_hlc_past_age must be positive")
	}
	if c.Limits.MaxOperationsPerBatch <= 0 || c.Limits.RequestsPerSecond <= 0 || c.Limits.RequestBurst <= 0 {
		return errors.New("operation batch and request-rate limits must be positive")
	}
	if c.SQLite.BusyTimeout.Value() <= 0 {
		return errors.New("sqlite.busy_timeout must be positive")
	}
	switch strings.ToUpper(c.SQLite.Synchronous) {
	case "OFF", "NORMAL", "FULL", "EXTRA":
	default:
		return fmt.Errorf("unsupported sqlite.synchronous %q", c.SQLite.Synchronous)
	}
	if c.Backups.KeepDaily < MinKeepDailyBackups || c.Backups.KeepDaily > MaxKeepDailyBackups {
		return fmt.Errorf("backups.keep_daily must be between %d and %d", MinKeepDailyBackups, MaxKeepDailyBackups)
	}
	if _, err := time.Parse("15:04", c.Backups.DailyAt); err != nil {
		return fmt.Errorf("backups.daily_at must use 24-hour HH:MM format: %w", err)
	}
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("unsupported logging.level %q", c.Logging.Level)
	}
	switch c.Logging.Format {
	case "json", "text":
	default:
		return fmt.Errorf("unsupported logging.format %q", c.Logging.Format)
	}
	if c.Metrics.Enabled {
		if _, _, err := net.SplitHostPort(c.Metrics.Listen); err != nil {
			return fmt.Errorf("metrics.listen must be a host:port address: %w", err)
		}
	}
	return nil
}

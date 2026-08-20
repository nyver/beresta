package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/beresta-app/beresta/server"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("beresta-server", flag.ContinueOnError)
	dataDirectory := flags.String("data", "", "directory containing all persistent server state")
	configFile := flags.String("config", "", "optional YAML configuration file")
	initializeOnly := flags.Bool("init-only", false, "initialize and verify persistent state, then exit")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	remaining := flags.Args()
	configPath := *configFile
	if configPath == "" {
		configDirectory := *dataDirectory
		if configDirectory == "" {
			configDirectory = "./data"
		}
		configPath = filepath.Join(configDirectory, "config.yaml")
	}
	cfg, err := server.LoadConfig(configPath, *dataDirectory)
	if err != nil {
		return err
	}
	configureLogger(cfg.Logging)
	runtime, err := server.Initialize(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer runtime.Close()

	command := "serve"
	if len(remaining) > 0 {
		command = remaining[0]
		remaining = remaining[1:]
	}
	if command != "serve" {
		return runCommand(context.Background(), runtime, command, remaining, stdout)
	}
	if len(remaining) != 0 {
		return fmt.Errorf("unexpected serve arguments: %v", remaining)
	}
	slog.Info("server initialized",
		"data_directory", runtime.DataDirectory,
		"listen", runtime.Config.Server.Listen,
		"tls_fingerprint_sha256", runtime.TLSIdentity.Fingerprint,
	)
	if *initializeOnly {
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runtime.Serve(ctx)
}

func runCommand(ctx context.Context, runtime *server.Runtime, command string, arguments []string, stdout io.Writer) error {
	switch command {
	case "invite":
		flags := flag.NewFlagSet("invite", flag.ContinueOnError)
		name := flags.String("name", "", "display name for the invited user")
		ttl := flags.Duration("ttl", runtime.Config.Auth.InviteTTL.Value(), "single-use invite lifetime")
		if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
			return commandArgumentsError(err, flags.Args())
		}
		invite, err := runtime.Storage.CreateInvite(ctx, *name, *ttl, time.Now())
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, invite.Code)
		return err
	case "users":
		if len(arguments) != 1 || arguments[0] != "list" {
			return errors.New("usage: beresta-server [global flags] users list")
		}
		users, err := runtime.Storage.AdminListUsers(ctx)
		if err != nil {
			return err
		}
		return writeCommandJSON(stdout, map[string]any{"users": users})
	case "device":
		if len(arguments) == 0 || arguments[0] != "revoke" {
			return errors.New("usage: beresta-server [global flags] device revoke --id <device-id>")
		}
		flags := flag.NewFlagSet("device revoke", flag.ContinueOnError)
		deviceID := flags.String("id", "", "device identifier")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 {
			return commandArgumentsError(err, flags.Args())
		}
		return runtime.Storage.AdminRevokeDevice(ctx, *deviceID, time.Now())
	case "backup":
		flags := flag.NewFlagSet("backup", flag.ContinueOnError)
		destination := flags.String("destination", runtime.Storage.ServerBackupDestination(), "backup directory")
		if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
			return commandArgumentsError(err, flags.Args())
		}
		backup, err := runtime.Storage.CreateServerBackup(ctx, *destination, "manual", time.Now())
		if err != nil {
			return err
		}
		return writeCommandJSON(stdout, backup)
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ContinueOnError)
		backupPath := flags.String("backup", "", "backup directory to verify instead of live state")
		if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
			return commandArgumentsError(err, flags.Args())
		}
		if *backupPath != "" {
			backup, err := server.VerifyServerBackup(*backupPath)
			if err != nil {
				return err
			}
			return writeCommandJSON(stdout, backup)
		}
		if err := runtime.Storage.VerifyServerState(ctx); err != nil {
			return err
		}
		return writeCommandJSON(stdout, map[string]string{"status": "ok"})
	case "restore":
		flags := flag.NewFlagSet("restore", flag.ContinueOnError)
		backupPath := flags.String("backup", "", "verified backup directory")
		confirm := flags.Bool("confirm", false, "replace active database and blobs")
		if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
			return commandArgumentsError(err, flags.Args())
		}
		backup, err := server.VerifyServerBackup(*backupPath)
		if err != nil {
			return err
		}
		if !*confirm {
			return writeCommandJSON(stdout, map[string]any{"dry_run": true, "backup": backup})
		}
		if _, err := runtime.Storage.CreateServerBackup(ctx, runtime.Storage.ServerBackupDestination(), "pre-restore", time.Now()); err != nil {
			return fmt.Errorf("create pre-restore safety backup: %w", err)
		}
		if err := runtime.RestoreServerBackup(*backupPath); err != nil {
			return err
		}
		return writeCommandJSON(stdout, map[string]string{"status": "restored"})
	case "gc":
		flags := flag.NewFlagSet("gc", flag.ContinueOnError)
		confirm := flags.Bool("confirm", false, "remove eligible data; without it only report")
		if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
			return commandArgumentsError(err, flags.Args())
		}
		dryRun := !*confirm
		completed, err := runtime.Storage.GarbageCollectBlobs(ctx, time.Now().Add(-30*24*time.Hour), dryRun)
		if err != nil {
			return err
		}
		staging, err := runtime.Storage.GarbageCollectStagingBlobs(ctx, time.Now().Add(-24*time.Hour), dryRun)
		if err != nil {
			return err
		}
		return writeCommandJSON(stdout, map[string]any{"dry_run": dryRun, "completed": completed, "staging": staging})
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func commandArgumentsError(parseError error, remaining []string) error {
	if parseError != nil {
		return parseError
	}
	return fmt.Errorf("unexpected command arguments: %v", remaining)
}

func writeCommandJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func configureLogger(cfg server.LoggingConfig) {
	level := slog.LevelInfo
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewJSONHandler(os.Stderr, options)
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stderr, options)
	}
	slog.SetDefault(slog.New(handler))
}

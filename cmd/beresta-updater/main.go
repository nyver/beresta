// beresta-updater applies a pre-downloaded, signed desktop installer after
// the main application has exited. Release builds inject the pinned Ed25519
// public key and current version through linker variables.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/beresta-app/beresta/internal/desktopupdate"
)

var (
	releasePublicKeyBase64 string
	currentVersion         = "0.1.0"
)

type processRunner struct{}

func (processRunner) Run(ctx context.Context, path string, args ...string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: beresta-updater <apply|rollback> [options]")
	}
	switch args[0] {
	case "apply":
		flags := flag.NewFlagSet("apply", flag.ContinueOnError)
		manifestPath := flags.String("manifest", "", "path to signed update manifest")
		installedPath := flags.String("installed", "", "path to installed beresta.exe")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *manifestPath == "" || *installedPath == "" {
			return errors.New("apply requires -manifest and -installed")
		}
		manifest, err := desktopupdate.LoadManifest(*manifestPath)
		if err != nil {
			return err
		}
		publicKey, err := releasePublicKey()
		if err != nil {
			return err
		}
		artifactPath := filepath.Join(filepath.Dir(*manifestPath), manifest.Artifact)
		return desktopupdate.Apply(ctx, manifest, artifactPath, *installedPath, currentVersion, publicKey, desktopupdate.AuthenticodeVerifier{}, processRunner{})
	case "rollback":
		flags := flag.NewFlagSet("rollback", flag.ContinueOnError)
		installedPath := flags.String("installed", "", "path to installed beresta.exe")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *installedPath == "" {
			return errors.New("rollback requires -installed")
		}
		return desktopupdate.Rollback(*installedPath)
	default:
		return fmt.Errorf("unknown updater command %q", args[0])
	}
}

func releasePublicKey() (ed25519.PublicKey, error) {
	encoded, err := base64.StdEncoding.DecodeString(releasePublicKeyBase64)
	if err != nil || len(encoded) != ed25519.PublicKeySize {
		return nil, errors.New("updater release public key is not configured")
	}
	return ed25519.PublicKey(encoded), nil
}

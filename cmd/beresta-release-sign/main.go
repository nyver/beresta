// beresta-release-sign builds and signs a desktop update manifest matching
// schema/update-manifest-v1.schema.json for one built installer artifact.
// It is release tooling only: it must run on a machine holding the private
// Ed25519 release key, never in a distributed client binary (see
// docs/desktop-updates.md, "Release signing").
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/beresta-app/beresta/internal/desktopupdate"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("beresta-release-sign", flag.ContinueOnError)
	artifactPath := flags.String("artifact", "", "path to the built installer or executable to publish")
	version := flags.String("version", "", "release version, e.g. 1.2.0")
	platform := flags.String("platform", "windows-amd64", "manifest platform field")
	output := flags.String("output", "", "path to write the signed manifest JSON (default: <artifact>.manifest.json)")
	detachedFile := flags.String("detached-file", "", "sign this file's raw bytes instead of building an update manifest (for example, a server release SHA256SUMS file); writes <file>.sig")
	privateKeyFlag := flags.String("private-key-base64", "", "base64-encoded 64-byte Ed25519 private key (prefer BERESTA_RELEASE_PRIVATE_KEY_BASE64 instead, to keep it out of shell history)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	privateKeyBase64 := *privateKeyFlag
	if privateKeyBase64 == "" {
		privateKeyBase64 = getenv("BERESTA_RELEASE_PRIVATE_KEY_BASE64")
	}
	if privateKeyBase64 == "" {
		return errors.New("the release private key must be provided via BERESTA_RELEASE_PRIVATE_KEY_BASE64 or -private-key-base64")
	}
	privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil || len(privateKeyBytes) != ed25519.PrivateKeySize {
		return errors.New("release private key must be a base64-encoded 64-byte Ed25519 private key")
	}
	privateKey := ed25519.PrivateKey(privateKeyBytes)

	if *detachedFile != "" {
		return signDetached(*detachedFile, privateKey, stdout)
	}
	if *artifactPath == "" || *version == "" {
		return errors.New("usage: beresta-release-sign -artifact <path> -version <semver> [-platform windows-amd64] [-output <path>], or -detached-file <path>")
	}

	digest, size, err := hashFile(*artifactPath)
	if err != nil {
		return fmt.Errorf("hash artifact: %w", err)
	}

	manifest := desktopupdate.Manifest{
		FormatVersion: desktopupdate.ManifestFormatVersion,
		Version:       *version,
		Platform:      *platform,
		Artifact:      filepath.Base(*artifactPath),
		SizeBytes:     size,
		SHA256:        digest,
	}
	payload, err := manifest.SignedPayload()
	if err != nil {
		return fmt.Errorf("build signed payload: %w", err)
	}
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))

	// Round-trip through LoadManifest's own strict decoder before publishing,
	// so a malformed manifest is caught here rather than at a client's
	// update check.
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	outputPath := *output
	if outputPath == "" {
		outputPath = *artifactPath + ".manifest.json"
	}
	if err := os.WriteFile(outputPath, encoded, 0o600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if _, err := desktopupdate.LoadManifest(outputPath); err != nil {
		return fmt.Errorf("published manifest failed self-verification: %w", err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	fmt.Fprintf(stdout, "wrote %s (sha256=%s, public_key_base64=%s)\n", outputPath, digest, base64.StdEncoding.EncodeToString(publicKey))
	return nil
}

// signDetached signs path's raw bytes and writes the base64-encoded Ed25519
// signature to path+".sig". It is generic release-artifact signing (for
// example, a server cross-build's SHA256SUMS file) independent of the
// Windows update-manifest schema the rest of this tool targets.
func signDetached(path string, privateKey ed25519.PrivateKey, stdout io.Writer) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, contents))
	signaturePath := path + ".sig"
	if err := os.WriteFile(signaturePath, []byte(signature+"\n"), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", signaturePath, err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	fmt.Fprintf(stdout, "wrote %s (public_key_base64=%s)\n", signaturePath, base64.StdEncoding.EncodeToString(publicKey))
	return nil
}

func hashFile(path string) (hexDigest string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	hasher := sha256.New()
	written, err := io.Copy(hasher, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

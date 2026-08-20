// Package desktopupdate verifies and applies signed Windows desktop updates.
package desktopupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const (
	ManifestFormatVersion = 1
	MaxManifestBytes      = 64 << 10
	MaxArtifactBytes      = 512 << 20
)

var (
	ErrInvalidManifest  = errors.New("invalid update manifest")
	ErrSignatureInvalid = errors.New("update signature is invalid")
	ErrArtifactInvalid  = errors.New("update artifact is invalid")
	ErrVersionNotNewer  = errors.New("update version is not newer")
	ErrPublisherInvalid = errors.New("update publisher signature is invalid")
	safeArtifactName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.exe$`)
)

// Manifest describes one signed installer. Signature authenticates every
// other field through SignedPayload; it is not part of its own payload.
type Manifest struct {
	FormatVersion int    `json:"format_version"`
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	Artifact      string `json:"artifact"`
	SizeBytes     int64  `json:"size_bytes"`
	SHA256        string `json:"sha256"`
	Signature     string `json:"signature"`
}

type signedFields struct {
	FormatVersion int    `json:"format_version"`
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	Artifact      string `json:"artifact"`
	SizeBytes     int64  `json:"size_bytes"`
	SHA256        string `json:"sha256"`
}

// SignedPayload returns the deterministic JSON bytes release tooling signs.
func (m Manifest) SignedPayload() ([]byte, error) {
	if err := m.validateFields(); err != nil {
		return nil, err
	}
	return json.Marshal(signedFields{
		FormatVersion: m.FormatVersion,
		Version:       m.Version,
		Platform:      m.Platform,
		Artifact:      m.Artifact,
		SizeBytes:     m.SizeBytes,
		SHA256:        strings.ToLower(m.SHA256),
	})
}

// LoadManifest strictly decodes one bounded manifest file.
func LoadManifest(path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open update manifest: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, MaxManifestBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read update manifest: %w", err)
	}
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: manifest exceeds %d bytes", ErrInvalidManifest, MaxManifestBytes)
	}
	var m Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode: %v", ErrInvalidManifest, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if err := m.validateFields(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON value", ErrInvalidManifest)
		}
		return fmt.Errorf("%w: trailing data: %v", ErrInvalidManifest, err)
	}
	return nil
}

func (m Manifest) validateFields() error {
	if m.FormatVersion != ManifestFormatVersion {
		return fmt.Errorf("%w: unsupported format version %d", ErrInvalidManifest, m.FormatVersion)
	}
	if _, err := parseVersion(m.Version); err != nil {
		return fmt.Errorf("%w: version: %v", ErrInvalidManifest, err)
	}
	if m.Platform != "windows-amd64" {
		return fmt.Errorf("%w: unsupported platform %q", ErrInvalidManifest, m.Platform)
	}
	if filepath.Base(m.Artifact) != m.Artifact || !safeArtifactName.MatchString(m.Artifact) {
		return fmt.Errorf("%w: artifact must be a safe executable base filename", ErrInvalidManifest)
	}
	if m.SizeBytes <= 0 || m.SizeBytes > MaxArtifactBytes {
		return fmt.Errorf("%w: artifact size is outside allowed bounds", ErrInvalidManifest)
	}
	hash, err := hex.DecodeString(m.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return fmt.Errorf("%w: sha256 must be 32-byte hexadecimal", ErrInvalidManifest)
	}
	return nil
}

// PublisherVerifier verifies the platform publisher signature after the
// pinned release signature and artifact hash have been accepted.
type PublisherVerifier interface {
	VerifyPublisher(context.Context, string) error
}

// PublisherVerifierFunc adapts a function for tests and platform wrappers.
type PublisherVerifierFunc func(context.Context, string) error

func (f PublisherVerifierFunc) VerifyPublisher(ctx context.Context, path string) error {
	return f(ctx, path)
}

// Verify authenticates manifest and artifact before any installation write.
func Verify(ctx context.Context, manifest Manifest, artifactPath, currentVersion string, publicKey ed25519.PublicKey, publisher PublisherVerifier) error {
	if err := manifest.validateFields(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" && manifest.Platform != "windows-amd64" {
		return fmt.Errorf("%w: platform mismatch", ErrInvalidManifest)
	}
	if filepath.Base(artifactPath) != manifest.Artifact {
		return fmt.Errorf("%w: artifact filename mismatch", ErrArtifactInvalid)
	}
	newVersion, _ := parseVersion(manifest.Version)
	installedVersion, err := parseVersion(currentVersion)
	if err != nil {
		return fmt.Errorf("%w: current version: %v", ErrInvalidManifest, err)
	}
	if compareVersion(newVersion, installedVersion) <= 0 {
		return ErrVersionNotNewer
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: release public key length", ErrSignatureInvalid)
	}
	payload, err := manifest.SignedPayload()
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, payload, signature) {
		return ErrSignatureInvalid
	}
	if err := verifyArtifact(artifactPath, manifest); err != nil {
		return err
	}
	if publisher == nil {
		return fmt.Errorf("%w: verifier is not configured", ErrPublisherInvalid)
	}
	if err := publisher.VerifyPublisher(ctx, artifactPath); err != nil {
		return fmt.Errorf("%w: %v", ErrPublisherInvalid, err)
	}
	return nil
}

func verifyArtifact(path string, manifest Manifest) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: stat: %v", ErrArtifactInvalid, err)
	}
	if !info.Mode().IsRegular() || info.Size() != manifest.SizeBytes {
		return fmt.Errorf("%w: type or size mismatch", ErrArtifactInvalid)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: open: %v", ErrArtifactInvalid, err)
	}
	defer f.Close()
	h := sha256.New()
	written, err := io.Copy(h, io.LimitReader(f, MaxArtifactBytes+1))
	if err != nil || written != manifest.SizeBytes {
		return fmt.Errorf("%w: read failure", ErrArtifactInvalid)
	}
	want, _ := hex.DecodeString(manifest.SHA256)
	if !bytes.Equal(h.Sum(nil), want) {
		return fmt.Errorf("%w: sha256 mismatch", ErrArtifactInvalid)
	}
	return nil
}

type numericVersion [3]uint64

func parseVersion(value string) (numericVersion, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return numericVersion{}, errors.New("must contain three numeric components")
	}
	var result numericVersion
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return numericVersion{}, errors.New("components must be canonical decimal integers")
		}
		component, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return numericVersion{}, errors.New("components must be 32-bit decimal integers")
		}
		result[i] = component
	}
	return result, nil
}

func compareVersion(a, b numericVersion) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

package desktopupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifyRejectsTamperingAndPublisherFailure(t *testing.T) {
	dir := retryTempDir(t)
	artifact := filepath.Join(dir, "beresta-installer.exe")
	if err := os.WriteFile(artifact, []byte("signed installer"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, publicKey := signedManifest(t, artifact, "0.2.0")
	acceptPublisher := PublisherVerifierFunc(func(context.Context, string) error { return nil })
	if err := Verify(context.Background(), manifest, artifact, "0.1.0", publicKey, acceptPublisher); err != nil {
		t.Fatalf("Verify(valid): %v", err)
	}

	if err := os.WriteFile(artifact, []byte("tampered install"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), manifest, artifact, "0.1.0", publicKey, acceptPublisher); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("Verify(tampered) error = %v, want %v", err, ErrArtifactInvalid)
	}

	if err := os.WriteFile(artifact, []byte("signed installer"), 0o600); err != nil {
		t.Fatal(err)
	}
	rejectPublisher := PublisherVerifierFunc(func(context.Context, string) error { return errors.New("untrusted") })
	if err := Verify(context.Background(), manifest, artifact, "0.1.0", publicKey, rejectPublisher); !errors.Is(err, ErrPublisherInvalid) {
		t.Fatalf("Verify(untrusted publisher) error = %v, want %v", err, ErrPublisherInvalid)
	}
}

func TestVerifyRejectsOldVersionUnknownFieldsAndTraversal(t *testing.T) {
	dir := retryTempDir(t)
	artifact := filepath.Join(dir, "beresta-installer.exe")
	if err := os.WriteFile(artifact, []byte("installer"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, publicKey := signedManifest(t, artifact, "0.1.0")
	if err := Verify(context.Background(), manifest, artifact, "0.1.0", publicKey, PublisherVerifierFunc(func(context.Context, string) error { return nil })); !errors.Is(err, ErrVersionNotNewer) {
		t.Fatalf("Verify(equal version) error = %v, want %v", err, ErrVersionNotNewer)
	}

	manifestPath := filepath.Join(dir, "update.json")
	if err := os.WriteFile(manifestPath, []byte(`{"format_version":1,"version":"0.2.0","platform":"windows-amd64","artifact":"../evil.exe","size_bytes":1,"sha256":"0000000000000000000000000000000000000000000000000000000000000000","signature":"","extra":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(manifestPath); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("LoadManifest(unknown/traversal) error = %v, want %v", err, ErrInvalidManifest)
	}

	manifest.Artifact = "beresta.exe:payload"
	if _, err := manifest.SignedPayload(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("SignedPayload(alternate data stream) error = %v, want %v", err, ErrInvalidManifest)
	}
}

func TestApplyPreservesAndRollsBackPriorExecutable(t *testing.T) {
	dir := retryTempDir(t)
	installed := filepath.Join(dir, "beresta.exe")
	artifact := filepath.Join(dir, "beresta-installer.exe")
	if err := os.WriteFile(installed, []byte("version one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("installer"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, publicKey := signedManifest(t, artifact, "0.2.0")
	publisher := PublisherVerifierFunc(func(context.Context, string) error { return nil })
	runner := InstallerRunnerFunc(func(_ context.Context, _ string, args ...string) error {
		if len(args) != 3 || args[0] != "/S" || args[1] != "/UPDATE" || args[2] != "/D="+dir {
			t.Fatalf("installer args = %v", args)
		}
		return os.WriteFile(installed, []byte("version two"), 0o700)
	})
	if err := Apply(context.Background(), manifest, artifact, installed, "0.1.0", publicKey, publisher, runner); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertFileContent(t, installed, "version two")
	assertFileContent(t, installed+rollbackSuffix, "version one")
	if err := Rollback(installed); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	assertFileContent(t, installed, "version one")
}

func TestApplyRejectsSuccessfulInstallerThatDoesNotReplaceExecutable(t *testing.T) {
	dir := retryTempDir(t)
	installed := filepath.Join(dir, "beresta.exe")
	artifact := filepath.Join(dir, "beresta-installer.exe")
	if err := os.WriteFile(installed, []byte("version one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("installer"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, publicKey := signedManifest(t, artifact, "0.2.0")
	publisher := PublisherVerifierFunc(func(context.Context, string) error { return nil })
	runner := InstallerRunnerFunc(func(context.Context, string, ...string) error { return nil })

	if err := Apply(context.Background(), manifest, artifact, installed, "0.1.0", publicKey, publisher, runner); err == nil {
		t.Fatal("Apply(no-op installer) error = nil")
	}
	assertFileContent(t, installed, "version one")
}

func TestApplyRestoresPriorExecutableWhenInstallerFails(t *testing.T) {
	dir := retryTempDir(t)
	installed := filepath.Join(dir, "beresta.exe")
	artifact := filepath.Join(dir, "beresta-installer.exe")
	_ = os.WriteFile(installed, []byte("version one"), 0o700)
	_ = os.WriteFile(artifact, []byte("installer"), 0o700)
	manifest, publicKey := signedManifest(t, artifact, "0.2.0")
	runner := InstallerRunnerFunc(func(context.Context, string, ...string) error {
		if err := os.WriteFile(installed, []byte("partial"), 0o700); err != nil {
			return err
		}
		return errors.New("installer exit 1")
	})
	err := Apply(context.Background(), manifest, artifact, installed, "0.1.0", publicKey, PublisherVerifierFunc(func(context.Context, string) error { return nil }), runner)
	if err == nil {
		t.Fatal("Apply(failing installer) error = nil")
	}
	assertFileContent(t, installed, "version one")
}

func TestApplyRestoresPriorExecutableWhenInstalledPublisherIsRejected(t *testing.T) {
	dir := retryTempDir(t)
	installed := filepath.Join(dir, "beresta.exe")
	artifact := filepath.Join(dir, "beresta-installer.exe")
	if err := os.WriteFile(installed, []byte("version one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("installer"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, publicKey := signedManifest(t, artifact, "0.2.0")
	publisher := PublisherVerifierFunc(func(_ context.Context, path string) error {
		if path == installed {
			return errors.New("installed executable has an untrusted publisher")
		}
		return nil
	})
	runner := InstallerRunnerFunc(func(context.Context, string, ...string) error {
		return os.WriteFile(installed, []byte("untrusted version two"), 0o700)
	})

	err := Apply(context.Background(), manifest, artifact, installed, "0.1.0", publicKey, publisher, runner)
	if err == nil {
		t.Fatal("Apply(untrusted installed executable) error = nil")
	}
	assertFileContent(t, installed, "version one")
}

func signedManifest(t *testing.T, artifact, version string) (Manifest, ed25519.PublicKey) {
	t.Helper()
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	manifest := Manifest{
		FormatVersion: ManifestFormatVersion,
		Version:       version,
		Platform:      "windows-amd64",
		Artifact:      filepath.Base(artifact),
		SizeBytes:     int64(len(data)),
		SHA256:        hex.EncodeToString(hash[:]),
	}
	payload, err := manifest.SignedPayload()
	if err != nil {
		t.Fatal(err)
	}
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return manifest, publicKey
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}

func retryTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "beresta-desktopupdate-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for {
			err := os.RemoveAll(dir)
			if err == nil {
				if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Errorf("remove updater test directory %s: %v", dir, err)
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	})
	return dir
}

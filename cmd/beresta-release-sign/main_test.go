package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/beresta-app/beresta/internal/desktopupdate"
)

func TestRunProducesASelfVerifyingSignedManifest(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "beresta.exe")
	if err := os.WriteFile(artifact, []byte("pretend installer bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyBase64 := base64.StdEncoding.EncodeToString(privateKey)

	var stdout bytes.Buffer
	env := map[string]string{"BERESTA_RELEASE_PRIVATE_KEY_BASE64": privateKeyBase64}
	if err := run([]string{"-artifact", artifact, "-version", "1.2.0"}, &stdout, func(key string) string { return env[key] }); err != nil {
		t.Fatalf("run: %v", err)
	}

	manifest, err := desktopupdate.LoadManifest(artifact + ".manifest.json")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if manifest.Version != "1.2.0" || manifest.Platform != "windows-amd64" || manifest.Artifact != "beresta.exe" {
		t.Fatalf("unexpected manifest fields: %+v", manifest)
	}

	// The signature this tool produced must satisfy the same Verify path
	// the real updater uses, against the public key paired with the
	// private key we signed with (a trusted publisher check is a separate,
	// platform-specific concern already covered by internal/desktopupdate's
	// own tests, so it is stubbed out here).
	allow := desktopupdate.PublisherVerifierFunc(func(context.Context, string) error { return nil })
	if err := desktopupdate.Verify(context.Background(), manifest, artifact, "1.1.0", publicKey, allow); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// A different public key must reject it.
	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := desktopupdate.Verify(context.Background(), manifest, artifact, "1.1.0", otherPublic, allow); err == nil {
		t.Fatal("expected verification against an unrelated public key to fail")
	}
}

func TestRunDetachedFileSignatureVerifies(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "SHA256SUMS")
	contents := []byte("deadbeef  beresta-server-linux-amd64\n")
	if err := os.WriteFile(target, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"BERESTA_RELEASE_PRIVATE_KEY_BASE64": base64.StdEncoding.EncodeToString(privateKey)}

	var stdout bytes.Buffer
	if err := run([]string{"-detached-file", target}, &stdout, func(key string) string { return env[key] }); err != nil {
		t.Fatalf("run: %v", err)
	}

	signatureBase64, err := os.ReadFile(target + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(signatureBase64)))
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(publicKey, contents, signature) {
		t.Fatal("detached signature does not verify against the file contents and paired public key")
	}
	if ed25519.Verify(publicKey, append(append([]byte(nil), contents...), '!'), signature) {
		t.Fatal("detached signature must not verify against tampered contents")
	}
}

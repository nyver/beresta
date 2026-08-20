package server

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureSelfSignedIdentityIsStableAndUsable(t *testing.T) {
	directory := t.TempDir()
	first, err := ensureSelfSignedIdentity(directory, "127.0.0.1:8443", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ensureSelfSignedIdentity(directory, "127.0.0.1:8443", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == "" || first.Fingerprint != second.Fingerprint {
		t.Fatalf("fingerprint changed across restart: %q != %q", first.Fingerprint, second.Fingerprint)
	}
	if len(strings.Split(first.Fingerprint, ":")) != 32 {
		t.Fatalf("fingerprint is not colon-delimited SHA-256: %q", first.Fingerprint)
	}
	certificatePEM, err := os.ReadFile(first.CertificateFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certificatePEM)
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := certificate.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatalf("generated certificate does not cover listener: %v", err)
	}
}

func TestEnsureSelfSignedIdentityRejectsPartialState(t *testing.T) {
	directory := t.TempDir()
	tlsDirectory := filepath.Join(directory, "tls")
	if err := os.Mkdir(tlsDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tlsDirectory, certificateFilename), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureSelfSignedIdentity(directory, "127.0.0.1:8443", time.Now()); err == nil {
		t.Fatal("partial TLS identity was silently replaced")
	}
}

func TestEnsureSelfSignedIdentityRemovesStalePrivateStagingDirectory(t *testing.T) {
	directory := t.TempDir()
	stale := filepath.Join(directory, ".tls-stale")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, privateKeyFilename), []byte("stale secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureSelfSignedIdentity(directory, "127.0.0.1:8443", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale TLS staging directory remains: %v", err)
	}
}

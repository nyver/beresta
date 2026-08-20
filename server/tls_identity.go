package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	certificateFilename = "server.crt"
	privateKeyFilename  = "server.key"
)

// TLSIdentity identifies the certificate used by the server listener.
type TLSIdentity struct {
	CertificateFile string
	PrivateKeyFile  string
	Fingerprint     string
}

func ensureTLSIdentity(dataDirectory string, cfg TLSConfig, listenAddress string) (TLSIdentity, error) {
	switch cfg.Mode {
	case "self_signed":
		return ensureSelfSignedIdentity(dataDirectory, listenAddress, time.Now())
	case "files":
		return loadTLSIdentity(cfg.CertificateFile, cfg.PrivateKeyFile)
	default:
		return TLSIdentity{}, fmt.Errorf("unsupported TLS mode %q", cfg.Mode)
	}
}

func ensureSelfSignedIdentity(dataDirectory, listenAddress string, now time.Time) (TLSIdentity, error) {
	tlsDirectory := filepath.Join(dataDirectory, "tls")
	certificatePath := filepath.Join(tlsDirectory, certificateFilename)
	privateKeyPath := filepath.Join(tlsDirectory, privateKeyFilename)

	certificateExists, err := regularFileExists(certificatePath)
	if err != nil {
		return TLSIdentity{}, err
	}
	privateKeyExists, err := regularFileExists(privateKeyPath)
	if err != nil {
		return TLSIdentity{}, err
	}
	if certificateExists != privateKeyExists {
		return TLSIdentity{}, errors.New("incomplete TLS identity: both tls/server.crt and tls/server.key must be present")
	}
	if certificateExists {
		if err := restrictDirectory(tlsDirectory); err != nil {
			return TLSIdentity{}, err
		}
		if err := restrictFile(certificatePath); err != nil {
			return TLSIdentity{}, err
		}
		if err := restrictFile(privateKeyPath); err != nil {
			return TLSIdentity{}, err
		}
		return loadTLSIdentity(certificatePath, privateKeyPath)
	}
	if _, err := os.Stat(tlsDirectory); err == nil {
		return TLSIdentity{}, errors.New("TLS directory exists without a complete identity")
	} else if !errors.Is(err, os.ErrNotExist) {
		return TLSIdentity{}, fmt.Errorf("inspect TLS directory: %w", err)
	}
	if err := cleanupTLSStagingDirectories(dataDirectory); err != nil {
		return TLSIdentity{}, err
	}

	certificatePEM, privateKeyPEM, err := generateSelfSignedCertificate(listenAddress, now)
	if err != nil {
		return TLSIdentity{}, err
	}
	stagingDirectory, err := os.MkdirTemp(dataDirectory, ".tls-")
	if err != nil {
		return TLSIdentity{}, fmt.Errorf("create TLS staging directory: %w", err)
	}
	defer os.RemoveAll(stagingDirectory)
	if err := restrictDirectory(stagingDirectory); err != nil {
		return TLSIdentity{}, err
	}
	stagedCertificate := filepath.Join(stagingDirectory, certificateFilename)
	stagedPrivateKey := filepath.Join(stagingDirectory, privateKeyFilename)
	if err := writeSyncedFile(stagedCertificate, certificatePEM, 0o600); err != nil {
		return TLSIdentity{}, err
	}
	if err := writeSyncedFile(stagedPrivateKey, privateKeyPEM, 0o600); err != nil {
		return TLSIdentity{}, err
	}
	if err := os.Rename(stagingDirectory, tlsDirectory); err != nil {
		return TLSIdentity{}, fmt.Errorf("publish TLS identity: %w", err)
	}
	if err := syncDirectory(dataDirectory); err != nil {
		return TLSIdentity{}, fmt.Errorf("sync published TLS identity: %w", err)
	}
	return loadTLSIdentity(certificatePath, privateKeyPath)
}

func generateSelfSignedCertificate(listenAddress string, now time.Time) ([]byte, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate TLS private key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("generate TLS serial: %w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	host, _, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return nil, nil, fmt.Errorf("parse server.listen for TLS identity: %w", err)
	}
	host = strings.Trim(host, "[]")
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Beresta Home Sync Server"},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.AddDate(5, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	if ip := net.ParseIP(host); ip != nil && !containsIP(template.IPAddresses, ip) {
		template.IPAddresses = append(template.IPAddresses, ip)
	} else if host != "" && host != "0.0.0.0" && host != "::" && !containsString(template.DNSNames, host) {
		template.DNSNames = append(template.DNSNames, host)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create self-signed TLS certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("encode TLS private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

func loadTLSIdentity(certificatePath, privateKeyPath string) (TLSIdentity, error) {
	certificatePEM, err := os.ReadFile(certificatePath)
	if err != nil {
		return TLSIdentity{}, fmt.Errorf("read TLS certificate: %w", err)
	}
	privateKeyPEM, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return TLSIdentity{}, fmt.Errorf("read TLS private key: %w", err)
	}
	if _, err := tls.X509KeyPair(certificatePEM, privateKeyPEM); err != nil {
		return TLSIdentity{}, fmt.Errorf("load TLS key pair: %w", err)
	}
	block, _ := pem.Decode(certificatePEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return TLSIdentity{}, errors.New("decode TLS certificate: missing CERTIFICATE block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return TLSIdentity{}, fmt.Errorf("parse TLS certificate: %w", err)
	}
	now := time.Now()
	if now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
		return TLSIdentity{}, fmt.Errorf("TLS certificate is not valid at the current time (valid from %s to %s)", certificate.NotBefore.Format(time.RFC3339), certificate.NotAfter.Format(time.RFC3339))
	}
	digest := sha256.Sum256(certificate.Raw)
	fingerprint := strings.ToUpper(strings.Join(splitEveryTwo(hex.EncodeToString(digest[:])), ":"))
	return TLSIdentity{
		CertificateFile: certificatePath,
		PrivateKeyFile:  privateKeyPath,
		Fingerprint:     fingerprint,
	}, nil
}

func cleanupTLSStagingDirectories(dataDirectory string) error {
	entries, err := os.ReadDir(dataDirectory)
	if err != nil {
		return fmt.Errorf("inspect TLS staging directories: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".tls-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dataDirectory, entry.Name())); err != nil {
			return fmt.Errorf("remove stale TLS staging directory: %w", err)
		}
	}
	return nil
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	return true, nil
}

func writeSyncedFile(path string, contents []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	remove := true
	defer func() {
		file.Close()
		if remove {
			os.Remove(path)
		}
	}()
	if _, err := file.Write(contents); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", filepath.Base(path), err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err := restrictFile(path); err != nil {
		return err
	}
	remove = false
	return nil
}

func splitEveryTwo(value string) []string {
	parts := make([]string, 0, len(value)/2)
	for index := 0; index < len(value); index += 2 {
		parts = append(parts, value[index:index+2])
	}
	return parts
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsIP(values []net.IP, target net.IP) bool {
	for _, value := range values {
		if value.Equal(target) {
			return true
		}
	}
	return false
}

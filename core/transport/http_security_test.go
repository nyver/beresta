package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

func TestPinnedTLSRejectsCertificateSubstitution(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"status":"ok","schema":"v1"}`))
			return
		}
		http.NotFound(writer, request)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()

	device, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(server.Certificate().Raw)
	config := HTTPConfig{BaseURL: server.URL, SecurityMode: HTTPSecurityPinned, PinnedFingerprint: hex.EncodeToString(digest[:]),
		DeviceID: device, SignChallenge: func([]byte) ([]byte, error) { return make([]byte, 64), nil }}
	client, err := NewHTTP(config)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics := client.Diagnose(t.Context()); !diagnostics.Reachable || !diagnostics.TLS13 {
		t.Fatalf("valid pinned certificate diagnostics = %+v", diagnostics)
	}
	config.PinnedFingerprint = hex.EncodeToString(make([]byte, sha256.Size))
	substituted, err := NewHTTP(config)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics := substituted.Diagnose(t.Context()); diagnostics.Reachable || diagnostics.ErrorClass != "certificate_mismatch" {
		t.Fatalf("substituted certificate diagnostics = %+v", diagnostics)
	}
}

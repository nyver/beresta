package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

func withNetworkInterfaces(t *testing.T, interfaces []interfaceState, err error) {
	t.Helper()
	original := listNetworkInterfaces
	listNetworkInterfaces = func() ([]interfaceState, error) { return interfaces, err }
	t.Cleanup(func() { listNetworkInterfaces = original })
}

func TestClassifyTransportErrorCertificateAndAuthentication(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"certificate pin mismatch", ErrCertificatePin, ErrorClassCertificateMismatch},
		{"certificate pin mismatch wrapped", errors.Join(errors.New("dial failed"), ErrCertificatePin), ErrorClassCertificateMismatch},
		{"authentication failed", ErrAuthentication, ErrorClassAuthenticationFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyTransportError(tt.err); got != tt.want {
				t.Fatalf("classifyTransportError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyTransportErrorNoNetworkWhenNoOtherInterfaceIsUp(t *testing.T) {
	withNetworkInterfaces(t, []interfaceState{
		{up: true, loopback: true, hasAddrs: true},   // loopback only: not "other" connectivity
		{up: false, loopback: false, hasAddrs: true}, // down: does not count
		{up: true, loopback: false, hasAddrs: false}, // up but no address: does not count
	}, nil)

	got := classifyTransportError(errors.New("dial tcp: connection timed out"))
	if got != ErrorClassNoNetwork {
		t.Fatalf("classifyTransportError = %q, want %q", got, ErrorClassNoNetwork)
	}
}

func TestClassifyTransportErrorServerUnavailableWhenOtherInterfaceIsUp(t *testing.T) {
	withNetworkInterfaces(t, []interfaceState{
		{up: true, loopback: true, hasAddrs: true},
		{up: true, loopback: false, hasAddrs: true}, // a real, working interface
	}, nil)

	got := classifyTransportError(errors.New("dial tcp: connection refused"))
	if got != ErrorClassServerUnavailable {
		t.Fatalf("classifyTransportError = %q, want %q", got, ErrorClassServerUnavailable)
	}
}

func TestClassifyTransportErrorFailsOpenOnInterfaceEnumerationError(t *testing.T) {
	withNetworkInterfaces(t, nil, errors.New("permission denied"))

	got := classifyTransportError(errors.New("dial tcp: connection timed out"))
	if got != ErrorClassServerUnavailable {
		t.Fatalf("classifyTransportError = %q, want %q (fail open, not no_network)", got, ErrorClassServerUnavailable)
	}
}

func TestHasOtherNetworkConnectivityOnRealHost(t *testing.T) {
	// A smoke test only: this environment's real interface set is outside
	// this package's control, so it only asserts the probe runs without
	// error and returns a bool, not a specific value.
	_ = hasOtherNetworkConnectivity()
}

func TestDiagnoseUnreachableServerReportsNoNetworkOrServerUnavailable(t *testing.T) {
	device, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	config := HTTPConfig{
		BaseURL:      "https://127.0.0.1:1", // reserved, always-refused port
		SecurityMode: HTTPSecurityTrusted, DeviceID: device,
		SignChallenge: func([]byte) ([]byte, error) { return make([]byte, 64), nil },
	}
	client, err := NewHTTP(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	diagnostics := client.Diagnose(ctx)
	if diagnostics.Reachable {
		t.Fatalf("diagnostics = %+v, want unreachable", diagnostics)
	}
	if diagnostics.ErrorClass != ErrorClassNoNetwork && diagnostics.ErrorClass != ErrorClassServerUnavailable {
		t.Fatalf("ErrorClass = %q, want %q or %q", diagnostics.ErrorClass, ErrorClassNoNetwork, ErrorClassServerUnavailable)
	}
}

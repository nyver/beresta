//go:build windows && cgo

package windowskeystore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/keystore"
	"golang.org/x/sys/windows"
)

func TestSystemVerifierAvailabilityABI(t *testing.T) {
	_, _, build := windows.RtlGetNtVersionNumbers()
	if build < minimumHelloInteropBuild {
		t.Skip("owner-window UserConsentVerifier interop requires Windows build 22000")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := (SystemVerifier{}).Available(ctx)
	if err != nil && !errors.Is(err, keystore.ErrUnavailable) {
		t.Fatalf("Available() error = %v", err)
	}
}

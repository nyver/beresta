//go:build windows && !cgo

package main

import (
	"context"

	"github.com/beresta-app/beresta/core/keystore"
	windowskeystore "github.com/beresta-app/beresta/desktop/platform/keystore"
)

// newKeyWrapper falls back to plain user-scoped DPAPI when built without
// cgo: Windows Hello verification (windowskeystore.HelloDPAPI /
// SystemVerifier) is implemented through a small C shim and is only
// available in a `windows && cgo` build (see
// desktop/platform/keystore/hello_system_windows.go). A production
// release always builds with cgo (SQLCipher itself requires it), so this
// path exists only to keep a cgo-free `go build`/`go vet` working for
// tooling, never to ship without Hello support.
func newKeyWrapper(_ context.Context, _ string) (keystore.Wrapper, string, error) {
	wrapper := windowskeystore.NewDPAPI()
	return wrapper, wrapper.Protection().String(), nil
}

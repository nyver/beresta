//go:build windows

package main

import (
	"context"

	"github.com/beresta-app/beresta/core/keystore"
	windowskeystore "github.com/beresta-app/beresta/desktop/platform/keystore"
)

// newKeyWrapper always selects plain user-scoped DPAPI. An earlier
// Windows Hello-gated mode (UserConsentVerifier via a small C shim) was
// removed: RequestVerificationForWindowAsync reliably crashed the process
// the moment the user completed verification, reproduced with two
// independently correct native consumption patterns (get_Status polling
// and put_Completed/CoWaitForMultipleHandles), which pointed at a
// platform-level issue rather than something fixable in this codebase.
func newKeyWrapper(_ context.Context, _ string) (keystore.Wrapper, string, error) {
	wrapper := windowskeystore.NewDPAPI()
	return wrapper, wrapper.Protection().String(), nil
}

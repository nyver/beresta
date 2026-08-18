//go:build windows && cgo

package windowskeystore

/*
#cgo windows LDFLAGS: -lruntimeobject
#include <stdint.h>
#include <windows.h>

HRESULT beresta_hello_available(uintptr_t cancel_event, INT32 *result);
HRESULT beresta_hello_verify(
    uintptr_t window,
    const wchar_t *message,
    UINT32 message_length,
    uintptr_t cancel_event,
    INT32 *result);
*/
import "C"

import (
	"context"
	"runtime"
	"unicode/utf16"
	"unsafe"

	"github.com/beresta-app/beresta/core/keystore"
	"golang.org/x/sys/windows"
)

const (
	helloAvailable = 0
	helloVerified  = 0
	helloCanceled  = 6
)

// SystemVerifier invokes Windows.Security.Credentials.UI.UserConsentVerifier.
// Verification is owner-window-bound and is supported from Windows build 22000.
type SystemVerifier struct{}

func (SystemVerifier) Available(ctx context.Context) error {
	var result C.INT32
	hr, err := callWithCancel(ctx, func(event windows.Handle) C.HRESULT {
		return C.beresta_hello_available(C.uintptr_t(event), &result)
	})
	if err != nil {
		return err
	}
	if failedHRESULT(hr) || int32(result) != helloAvailable {
		return keystore.ErrUnavailable
	}
	return nil
}

func (SystemVerifier) Verify(ctx context.Context, hwnd uintptr, prompt string) error {
	if hwnd == 0 || len(prompt) == 0 || len(prompt) > 256 {
		return keystore.ErrUnavailable
	}
	message := utf16.Encode([]rune(prompt))
	if len(message) == 0 || len(message) > 256 {
		return keystore.ErrUnavailable
	}
	defer clear(message)

	var result C.INT32
	hr, err := callWithCancel(ctx, func(event windows.Handle) C.HRESULT {
		return C.beresta_hello_verify(
			C.uintptr_t(hwnd),
			(*C.wchar_t)(unsafe.Pointer(&message[0])),
			C.UINT32(len(message)),
			C.uintptr_t(event),
			&result,
		)
	})
	if err != nil {
		return err
	}
	if failedHRESULT(hr) {
		return keystore.ErrUnavailable
	}
	switch int32(result) {
	case helloVerified:
		return nil
	case helloCanceled:
		return keystore.ErrCanceled
	default:
		return keystore.ErrAuthentication
	}
}

func callWithCancel(ctx context.Context, call func(windows.Handle) C.HRESULT) (C.HRESULT, error) {
	if err := ctx.Err(); err != nil {
		return 0, keystore.ErrCanceled
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, keystore.WrapError("create cancellation event", err)
	}
	defer windows.CloseHandle(event)

	done := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = windows.SetEvent(event)
		case <-done:
		}
	}()
	runtime.LockOSThread()
	hr := call(event)
	runtime.UnlockOSThread()
	close(done)
	<-watcherDone
	if ctx.Err() != nil {
		return hr, keystore.ErrCanceled
	}
	return hr, nil
}

func failedHRESULT(value C.HRESULT) bool {
	return int32(value) < 0
}

var _ UserVerifier = SystemVerifier{}

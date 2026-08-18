//go:build windows

package windowskeystore

import (
	"context"
	"errors"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
	"golang.org/x/sys/windows"
)

const minimumHelloInteropBuild = 22000

// UserVerifier abstracts the OS prompt so policy and cancellation paths can be
// tested without displaying Windows UI.
type UserVerifier interface {
	Available(context.Context) error
	Verify(context.Context, uintptr, string) error
}

// HelloDPAPI requires Windows Hello verification before unwrapping a DPAPI
// envelope. The envelope records this mode so it cannot be opened as fallback.
type HelloDPAPI struct {
	dpapi    *DPAPI
	verifier UserVerifier
	hwnd     func() uintptr
	prompt   string
}

func NewHelloDPAPI(verifier UserVerifier, hwnd func() uintptr, prompt string) (*HelloDPAPI, error) {
	if verifier == nil || hwnd == nil || len(prompt) == 0 || len(prompt) > 256 {
		return nil, keystore.ErrUnavailable
	}
	return &HelloDPAPI{
		dpapi:    newDPAPI(keystore.ProtectionWindowsHello),
		verifier: verifier,
		hwnd:     hwnd,
		prompt:   prompt,
	}, nil
}

func (h *HelloDPAPI) Protection() keystore.Protection {
	return keystore.ProtectionWindowsHello
}

func (h *HelloDPAPI) Wrap(ctx context.Context, metadata keystore.Metadata, secret *corecrypto.Secret) ([]byte, error) {
	if err := h.verifier.Available(ctx); err != nil {
		return nil, normalizeVerifierError(err)
	}
	return h.dpapi.Wrap(ctx, metadata, secret)
}

func (h *HelloDPAPI) Unwrap(ctx context.Context, metadata keystore.Metadata, encoded []byte) (*corecrypto.Secret, error) {
	if _, err := keystore.OpenEnvelope(encoded, h.Protection(), metadata); err != nil {
		return nil, err
	}
	if err := h.verifier.Available(ctx); err != nil {
		return nil, normalizeVerifierError(err)
	}
	if err := h.verifier.Verify(ctx, h.hwnd(), h.prompt); err != nil {
		return nil, normalizeVerifierError(err)
	}
	return h.dpapi.Unwrap(ctx, metadata, encoded)
}

func (h *HelloDPAPI) Delete(ctx context.Context, metadata keystore.Metadata) error {
	return h.dpapi.Delete(ctx, metadata)
}

// Recommended selects Hello on Windows 11+ only when it is configured and
// usable. All other supported Windows installations receive explicit DPAPI.
func Recommended(ctx context.Context, verifier UserVerifier, hwnd func() uintptr, prompt string) (keystore.Wrapper, error) {
	_, _, build := windows.RtlGetNtVersionNumbers()
	if build >= minimumHelloInteropBuild && verifier != nil {
		hello, err := NewHelloDPAPI(verifier, hwnd, prompt)
		if err == nil && hello.verifier.Available(ctx) == nil {
			return hello, nil
		}
		if ctx.Err() != nil {
			return nil, keystore.ErrCanceled
		}
	}
	return NewDPAPI(), nil
}

func normalizeVerifierError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, keystore.ErrCanceled):
		return keystore.ErrCanceled
	case errors.Is(err, keystore.ErrAuthentication):
		return keystore.ErrAuthentication
	default:
		return keystore.ErrUnavailable
	}
}

var _ keystore.Wrapper = (*HelloDPAPI)(nil)

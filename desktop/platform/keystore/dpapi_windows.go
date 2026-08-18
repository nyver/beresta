//go:build windows

package windowskeystore

import (
	"context"
	"unsafe"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
	"golang.org/x/sys/windows"
)

const dpapiDescription = "Beresta local device key"

// DPAPI wraps keys for the current Windows user without displaying system UI.
type DPAPI struct {
	protection keystore.Protection
}

// NewDPAPI creates the explicit Windows 10-compatible fallback adapter.
func NewDPAPI() *DPAPI {
	return newDPAPI(keystore.ProtectionWindowsDPAPI)
}

func newDPAPI(protection keystore.Protection) *DPAPI {
	return &DPAPI{protection: protection}
}

func (d *DPAPI) Protection() keystore.Protection {
	return d.protection
}

func (d *DPAPI) Wrap(ctx context.Context, metadata keystore.Metadata, secret *corecrypto.Secret) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, keystore.ErrCanceled
	}
	binding, err := keystore.Binding(d.protection, metadata)
	if err != nil {
		return nil, err
	}
	defer clear(binding)

	var protected []byte
	err = secret.Use(func(plaintext []byte) error {
		input := blob(plaintext)
		entropy := blob(binding)
		var output windows.DataBlob
		description, conversionErr := windows.UTF16PtrFromString(dpapiDescription)
		if conversionErr != nil {
			return conversionErr
		}
		if protectErr := windows.CryptProtectData(
			&input,
			description,
			&entropy,
			0,
			nil,
			windows.CRYPTPROTECT_UI_FORBIDDEN,
			&output,
		); protectErr != nil {
			return protectErr
		}
		defer freeDataBlob(&output, true)
		protected = append([]byte(nil), unsafe.Slice(output.Data, int(output.Size))...)
		return nil
	})
	if err != nil {
		return nil, keystore.WrapError("DPAPI wrap", err)
	}
	encoded, err := keystore.SealEnvelope(d.protection, metadata, protected)
	clear(protected)
	return encoded, err
}

func (d *DPAPI) Unwrap(ctx context.Context, metadata keystore.Metadata, encoded []byte) (*corecrypto.Secret, error) {
	if err := ctx.Err(); err != nil {
		return nil, keystore.ErrCanceled
	}
	protected, err := keystore.OpenEnvelope(encoded, d.protection, metadata)
	if err != nil {
		return nil, err
	}
	defer clear(protected)
	binding, err := keystore.Binding(d.protection, metadata)
	if err != nil {
		return nil, err
	}
	defer clear(binding)

	input := blob(protected)
	entropy := blob(binding)
	var output windows.DataBlob
	var description *uint16
	err = windows.CryptUnprotectData(
		&input,
		&description,
		&entropy,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&output,
	)
	if description != nil {
		_, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(description))))
	}
	if err != nil {
		return nil, keystore.ErrAuthentication
	}
	defer freeDataBlob(&output, true)
	if output.Size == 0 || output.Size > corecrypto.MaxSecretBytes {
		return nil, keystore.ErrAuthentication
	}
	plaintext := append([]byte(nil), unsafe.Slice(output.Data, int(output.Size))...)
	secret, err := corecrypto.TakeSecret(plaintext)
	if err != nil {
		clear(plaintext)
		return nil, keystore.WrapError("take DPAPI plaintext", err)
	}
	return secret, nil
}

// Delete is a no-op because DPAPI stores no named key material. Removing the
// returned envelope makes the wrapped key irrecoverable through this adapter.
func (d *DPAPI) Delete(ctx context.Context, metadata keystore.Metadata) error {
	if err := ctx.Err(); err != nil {
		return keystore.ErrCanceled
	}
	return metadata.Validate()
}

func blob(value []byte) windows.DataBlob {
	if len(value) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
}

func freeDataBlob(value *windows.DataBlob, wipe bool) {
	if value == nil || value.Data == nil {
		return
	}
	if wipe && value.Size > 0 {
		clear(unsafe.Slice(value.Data, int(value.Size)))
	}
	_, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(value.Data))))
	value.Data = nil
	value.Size = 0
}

var _ keystore.Wrapper = (*DPAPI)(nil)

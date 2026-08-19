package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/beresta-app/beresta/core/account"
	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
	"github.com/beresta-app/beresta/core/store"
)

// AppError is the shape every bound method returns for a known failure: a
// stable machine-readable Code the frontend can switch on (for example, to
// show a localized retry prompt or a "wrong passphrase" hint) plus a
// diagnostic Message that is never the sole way to distinguish cases,
// since messages are not covered by any localization or stability
// contract.
type AppError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error encodes the AppError as JSON rather than returning Message alone.
// Wails v2's binding dispatcher rejects the frontend's promise with only
// the plain string from err.Error() (see
// wailsapp/wails/v2/internal/frontend/dispatcher.processCallMessage); it
// never transmits a Go error's structured fields. Encoding Code and
// Message together here, and decoding them back out in the frontend's api
// helper (desktop/frontend/src/api.ts), is the only way the Code survives
// the bridge intact.
func (e *AppError) Error() string {
	data, err := json.Marshal(e)
	if err != nil {
		// e's fields are always plain strings, so Marshal cannot fail; this
		// is an unreachable fallback, not a real error path.
		return e.Message
	}
	return string(data)
}

// Known AppError codes. Frontend logic must switch on these, never on
// Message text.
const (
	ErrCodeLocked               = "locked"
	ErrCodeAccountExists        = "account_exists"
	ErrCodeNoLocalAccount       = "no_local_account"
	ErrCodeUnlockFailed         = "unlock_failed"
	ErrCodeUnknownWorkspace     = "unknown_workspace"
	ErrCodeNotFound             = "not_found"
	ErrCodeWrongWorkspace       = "wrong_workspace"
	ErrCodeInvalidInput         = "invalid_input"
	ErrCodeKeystoreUnavailable  = "keystore_unavailable"
	ErrCodeAuthenticationFailed = "authentication_failed"
	ErrCodeCanceled             = "canceled"
	ErrCodeKeyInvalidated       = "key_invalidated"
	ErrCodeBackupCorrupt        = "backup_corrupt"
	ErrCodeInsufficientSpace    = "insufficient_space"
	ErrCodeAlreadyExists        = "already_exists"
	ErrCodeInternal             = "internal"
)

// ErrLocked reports that a bound method requiring an unlocked account was
// called while the app has no account open.
var ErrLocked = errors.New("app: account is locked")

// mapError translates a core-layer error into a stable AppError the
// frontend can branch on. Errors already carrying an *AppError (from an
// inner call) pass through unchanged. A nil error stays nil, and an
// unrecognized error becomes ErrCodeInternal with its original text,
// which is safe here because every message on the account/store/keystore
// error paths is already a fixed diagnostic string, never raw secret
// material (secrets never leave those packages as error values).
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return err
	}

	switch {
	case errors.Is(err, ErrLocked), errors.Is(err, account.ErrAccountLocked):
		return &AppError{Code: ErrCodeLocked, Message: "The account is locked."}
	case errors.Is(err, account.ErrAccountExists):
		return &AppError{Code: ErrCodeAccountExists, Message: "An account already exists at this location."}
	case errors.Is(err, account.ErrNoLocalAccount):
		return &AppError{Code: ErrCodeNoLocalAccount, Message: "No local account exists at this location."}
	case errors.Is(err, account.ErrUnknownWorkspace):
		return &AppError{Code: ErrCodeUnknownWorkspace, Message: "Unknown workspace."}
	case errors.Is(err, account.ErrBackupCorrupt):
		return &AppError{Code: ErrCodeBackupCorrupt, Message: "This backup failed verification and cannot be restored."}
	case errors.Is(err, account.ErrInsufficientBackupCapacity):
		return &AppError{Code: ErrCodeInsufficientSpace, Message: "Not enough free space at the backup destination."}
	case errors.Is(err, corecrypto.ErrKeybagUnlock):
		// Deliberately uniform: a wrong passphrase and a corrupt keybag are
		// indistinguishable by design (docs/crypto-spec.md).
		return &AppError{Code: ErrCodeUnlockFailed, Message: "The passphrase is incorrect, or this account's data is unreadable."}
	case errors.Is(err, store.ErrNotFound):
		return &AppError{Code: ErrCodeNotFound, Message: "The requested item was not found."}
	case errors.Is(err, store.ErrWrongWorkspace):
		return &AppError{Code: ErrCodeWrongWorkspace, Message: "That item does not belong to this workspace."}
	case errors.Is(err, store.ErrNotebookCycle), errors.Is(err, store.ErrInvalidName), errors.Is(err, store.ErrEmptySearchQuery), errors.Is(err, store.ErrUnknownSearchTag):
		return &AppError{Code: ErrCodeInvalidInput, Message: err.Error()}
	case errors.Is(err, account.ErrInvalidAttachmentMetadata), errors.Is(err, account.ErrAttachmentBlobOrphaned):
		return &AppError{Code: ErrCodeInvalidInput, Message: err.Error()}
	case errors.Is(err, corecrypto.ErrAttachmentResourceLimit):
		return &AppError{Code: ErrCodeInvalidInput, Message: "This file is too large to attach."}
	case errors.Is(err, keystore.ErrUnavailable):
		return &AppError{Code: ErrCodeKeystoreUnavailable, Message: "Windows key protection is unavailable on this device."}
	case errors.Is(err, keystore.ErrAuthentication):
		return &AppError{Code: ErrCodeAuthenticationFailed, Message: "Windows Hello verification failed."}
	case errors.Is(err, keystore.ErrCanceled), errors.Is(err, context.Canceled):
		return &AppError{Code: ErrCodeCanceled, Message: "The operation was canceled."}
	case errors.Is(err, keystore.ErrKeyInvalidated):
		return &AppError{Code: ErrCodeKeyInvalidated, Message: "The platform key protecting this account was invalidated; the account must be re-created."}
	case errors.Is(err, context.DeadlineExceeded):
		return &AppError{Code: ErrCodeCanceled, Message: "The operation timed out."}
	default:
		return &AppError{Code: ErrCodeInternal, Message: err.Error()}
	}
}

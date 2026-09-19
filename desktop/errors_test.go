package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/store"
)

func TestAppErrorErrorEncodesJSONForTheFrontendBridge(t *testing.T) {
	appErr := &AppError{Code: ErrCodeInvalidInput, Message: "bad input"}

	var decoded struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(appErr.Error()), &decoded); err != nil {
		t.Fatalf("AppError.Error() is not valid JSON: %v (%q)", err, appErr.Error())
	}
	if decoded.Code != ErrCodeInvalidInput || decoded.Message != "bad input" {
		t.Fatalf("decoded = %+v, want code=%q message=%q", decoded, ErrCodeInvalidInput, "bad input")
	}
}

func TestMapErrorResultAlwaysEncodesAsJSON(t *testing.T) {
	cases := []error{
		account.ErrAccountLocked,
		account.ErrAccountExists,
		errors.New("some unrecognized internal failure"),
	}
	for _, err := range cases {
		mapped := mapError(err)
		var decoded struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if jsonErr := json.Unmarshal([]byte(mapped.Error()), &decoded); jsonErr != nil {
			t.Errorf("mapError(%v).Error() is not valid JSON: %v (%q)", err, jsonErr, mapped.Error())
		}
		if decoded.Code == "" {
			t.Errorf("mapError(%v) decoded with empty code", err)
		}
	}
}

// TestMapErrorReportsDistinctCodesForAttachmentPreflightFailures covers
// task 5.1's preflight checks: an oversized source and an out-of-space
// destination must map to their own distinct, localized AppError codes
// rather than collapsing into the generic internal error.
func TestMapErrorReportsDistinctCodesForAttachmentPreflightFailures(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{account.ErrAttachmentTooLarge, ErrCodeInvalidInput},
		{account.ErrInsufficientAttachmentCapacity, ErrCodeInsufficientAttachmentSpace},
	}
	for _, tc := range cases {
		if !isAppErrorCode(mapError(tc.err), tc.want) {
			t.Errorf("mapError(%v).Code = %v, want %v", tc.err, mapError(tc.err), tc.want)
		}
	}
}

// TestMapErrorReportsNotebookCycleDistinctlyFromInvalidInput covers task
// 6.3's invalid-move feedback: dropping (or menu-moving) a notebook into
// its own descendant must surface a code the frontend can localize into a
// specific explanation, not the generic "that value is not valid" message
// shared by every other invalid_input case.
func TestMapErrorReportsNotebookCycleDistinctlyFromInvalidInput(t *testing.T) {
	if !isAppErrorCode(mapError(store.ErrNotebookCycle), ErrCodeNotebookCycle) {
		t.Errorf("mapError(store.ErrNotebookCycle).Code = %v, want %v", mapError(store.ErrNotebookCycle), ErrCodeNotebookCycle)
	}
	if isAppErrorCode(mapError(store.ErrNotebookCycle), ErrCodeInvalidInput) {
		t.Errorf("mapError(store.ErrNotebookCycle) still reports the generic %v code", ErrCodeInvalidInput)
	}
	// store.ErrInvalidName must still map to the generic code: only the
	// cycle case gets its own.
	if !isAppErrorCode(mapError(store.ErrInvalidName), ErrCodeInvalidInput) {
		t.Errorf("mapError(store.ErrInvalidName).Code = %v, want %v", mapError(store.ErrInvalidName), ErrCodeInvalidInput)
	}
}

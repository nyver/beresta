package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/beresta-app/beresta/core/account"
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

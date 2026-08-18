// Package diagnostics emits allow-listed operational and crash metadata.
// It deliberately never serializes error or recovered panic values.
package diagnostics

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
)

var ErrInvalidEvent = errors.New("diagnostics: invalid event")

const maxTokenBytes = 128

// Event is the complete allow-list for one structured diagnostic record.
type Event struct {
	Component  string
	Operation  string
	ErrorClass string
	RequestID  string
	// Cause is accepted for caller convenience but is never serialized because
	// error strings may contain content, paths, passphrases, or key material.
	Cause error
}

type eventRecord struct {
	Component  string `json:"component"`
	Operation  string `json:"operation"`
	ErrorClass string `json:"error_class"`
	RequestID  string `json:"request_id,omitempty"`
}

// Encode writes one JSON record without accepting plaintext or raw errors.
func Encode(writer io.Writer, event Event) error {
	if writer == nil || !validToken(event.Component) || !validToken(event.Operation) ||
		!validToken(event.ErrorClass) || (event.RequestID != "" && !validToken(event.RequestID)) {
		return ErrInvalidEvent
	}
	return json.NewEncoder(writer).Encode(event)
}

// MarshalJSON preserves redaction even if a caller uses encoding/json
// directly instead of Encode.
func (event Event) MarshalJSON() ([]byte, error) {
	if !validToken(event.Component) || !validToken(event.Operation) ||
		!validToken(event.ErrorClass) || (event.RequestID != "" && !validToken(event.RequestID)) {
		return nil, ErrInvalidEvent
	}
	return json.Marshal(eventRecord{
		Component:  event.Component,
		Operation:  event.Operation,
		ErrorClass: event.ErrorClass,
		RequestID:  event.RequestID,
	})
}

// CrashMetadata reports only the recovered value's type, never its value.
func CrashMetadata(recovered any) map[string]string {
	kind := "nil"
	if recovered != nil {
		kind = reflect.TypeOf(recovered).String()
	}
	return map[string]string{"panic_type": kind}
}

func validToken(value string) bool {
	if len(value) == 0 || len(value) > maxTokenBytes {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}

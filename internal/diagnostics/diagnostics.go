// Package diagnostics emits allow-listed operational and crash metadata,
// and renders the user-facing "Copy diagnostics" bundle from
// core/presentation's fixed DiagnosticSummary/TechnicalDiagnostics
// schemas. It deliberately never serializes error or recovered panic
// values, note content, or other free-form input.
package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/beresta-app/beresta/core/presentation"
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

// forbiddenFieldSubstrings are lowercase substrings that must never appear
// in a diagnostics struct's field name. CopyDiagnostics only ever accepts
// the two fixed, reviewed struct types below, so this is defense-in-depth
// against a future field added to one of them without updating this
// check - exactly the class of mistake specs/product-experience's
// "Layered privacy-preserving diagnostics" requirement guards against
// (note content and titles, search queries, sensitive attachment names,
// passwords, keys, tokens, invite codes, plaintext exports, and clipboard
// content must never appear in a diagnostics bundle).
var forbiddenFieldSubstrings = []string{
	"password", "passphrase", "secret", "token", "key", "cipher", "nonce",
	"signature", "invite", "plaintext", "clipboard", "content", "title",
	"query", "name", "path",
}

// checkNoForbiddenFields recursively walks t's fields (including embedded
// and nested struct fields) and returns an error naming the first field
// whose name contains a forbidden substring.
func checkNoForbiddenFields(t reflect.Type) error {
	if t.Kind() != reflect.Struct {
		return nil
	}
	for i := range t.NumField() {
		field := t.Field(i)
		lower := strings.ToLower(field.Name)
		for _, bad := range forbiddenFieldSubstrings {
			if strings.Contains(lower, bad) {
				return fmt.Errorf("diagnostics: field %s.%s looks like it could carry a secret or content value and must not be exposed", t.Name(), field.Name)
			}
		}
		fieldType := field.Type
		for fieldType.Kind() == reflect.Ptr || fieldType.Kind() == reflect.Slice {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct && fieldType != reflect.TypeOf(time.Time{}) {
			if err := checkNoForbiddenFields(fieldType); err != nil {
				return err
			}
		}
	}
	return nil
}

// CopyDiagnostics renders summary and technical as the plain-text bundle
// the "Copy diagnostics" action places on the clipboard. It only accepts
// these two fixed, allowlisted schemas - never an arbitrary request,
// response, or error object - and re-validates their field names on every
// call so a future field that should never have been added here fails
// closed instead of silently shipping.
func CopyDiagnostics(summary presentation.DiagnosticSummary, technical presentation.TechnicalDiagnostics) (string, error) {
	if err := checkNoForbiddenFields(reflect.TypeOf(summary)); err != nil {
		return "", err
	}
	if err := checkNoForbiddenFields(reflect.TypeOf(technical)); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("Beresta diagnostics\n\n")
	b.WriteString("App version: " + summary.AppVersion + "\n")
	b.WriteString("Platform: " + summary.Platform + "\n")
	b.WriteString("Sync configured: " + strconv.FormatBool(summary.SyncConfigured) + "\n")
	b.WriteString("Last successful sync: " + formatTime(summary.LastSuccessfulSync) + "\n")
	b.WriteString("Pending changes: " + strconv.Itoa(summary.PendingCount) + "\n")
	b.WriteString("Connection state: " + string(summary.ConnectionState) + "\n")
	b.WriteString("Backup health: " + string(summary.Backup.Health) + "\n")
	b.WriteString("Backup last verified: " + formatTime(summary.Backup.LastVerified) + "\n")
	b.WriteString("Backup location: " + summary.Backup.Location + "\n")
	b.WriteString("Storage usage bytes: " + strconv.FormatInt(summary.StorageUsageBytes, 10) + "\n")
	b.WriteString("Database health: " + string(summary.Database) + "\n")
	b.WriteString("Update status: " + string(summary.Update) + "\n")

	b.WriteString("\nTechnical details\n\n")
	b.WriteString("Workspace ID: " + technical.WorkspaceID + "\n")
	b.WriteString("Device ID: " + technical.DeviceID + "\n")
	b.WriteString("Last error class: " + technical.LastErrorClass + "\n")
	b.WriteString("Pending operation count: " + strconv.Itoa(technical.PendingOperationCount) + "\n")
	b.WriteString("Quarantined operation IDs: " + strings.Join(technical.QuarantinedOperationIDs, ", ") + "\n")
	b.WriteString("Cursor sequence: " + strconv.FormatUint(technical.CursorSequence, 10) + "\n")
	b.WriteString("Cursor epoch: " + strconv.FormatUint(uint64(technical.CursorEpoch), 10) + "\n")
	b.WriteString("Retry count: " + strconv.Itoa(technical.RetryCount) + "\n")
	b.WriteString("Retry in: " + technical.RetryIn.String() + "\n")
	b.WriteString("Transport protocol: " + technical.TransportProtocol + "\n")
	b.WriteString("Transport security mode: " + technical.TransportSecurityMode + "\n")
	b.WriteString("Transport URL: " + technical.TransportURL + "\n")
	b.WriteString("Migration version: " + strconv.Itoa(technical.MigrationVersion) + "\n")
	return b.String(), nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format(time.RFC3339)
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

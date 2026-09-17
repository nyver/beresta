package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/presentation"
)

func TestSeededSecretsAreAbsentFromLogsAndCrashMetadata(t *testing.T) {
	seed := "seeded-secret-log-crash-canary"
	rawError := fmt.Errorf("unlock failed for %s", seed)

	var output bytes.Buffer
	if err := Encode(&output, Event{
		Component:  "crypto",
		Operation:  "unlock",
		ErrorClass: "authentication",
		RequestID:  "request-01",
		Cause:      rawError,
	}); err != nil {
		t.Fatal(err)
	}
	crash, err := json.Marshal(CrashMetadata(errors.New(seed)))
	if err != nil {
		t.Fatal(err)
	}
	combined := output.String() + string(crash)
	direct, err := json.Marshal(Event{
		Component: "crypto", Operation: "unlock", ErrorClass: "authentication", Cause: rawError,
	})
	if err != nil {
		t.Fatal(err)
	}
	combined += string(direct)
	if strings.Contains(combined, seed) {
		t.Fatalf("diagnostics exposed seeded secret: %s", combined)
	}
	if !strings.Contains(combined, "authentication") || !strings.Contains(combined, "errorString") {
		t.Fatalf("diagnostics omitted safe classification/type metadata: %s", combined)
	}
}

func TestDiagnosticsRejectFreeFormValues(t *testing.T) {
	var output bytes.Buffer
	err := Encode(&output, Event{Component: "crypto", Operation: "unlock", ErrorClass: "secret in class"})
	if !errors.Is(err, ErrInvalidEvent) || output.Len() != 0 {
		t.Fatalf("Encode() output=%q error=%v", output.String(), err)
	}
}

// TestCopyDiagnosticsIncludesEveryAllowlistedField covers task 4.1: the
// copied bundle must actually carry every field specs/product-experience's
// "Layered privacy-preserving diagnostics" requirement lists, not just a
// subset, or a support conversation would be missing exactly the state it
// needs.
func TestCopyDiagnosticsIncludesEveryAllowlistedField(t *testing.T) {
	summary := presentation.DiagnosticSummary{
		AppVersion: "1.2.3", Platform: "windows", SyncConfigured: true,
		LastSuccessfulSync: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		PendingCount:       7,
		ConnectionState:    presentation.SyncStateOffline,
		Backup: presentation.BackupStatus{
			Health: presentation.BackupHealthHealthy, LastVerified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Location: "external drive",
		},
		StorageUsageBytes: 1024,
		Database:          presentation.DatabaseHealthOK,
		Update:            presentation.UpdateStatusUpToDate,
	}
	technical := presentation.TechnicalDiagnostics{
		WorkspaceID: "ws-canary", DeviceID: "device-canary", LastErrorClass: "transient_transport",
		PendingOperationCount: 7, QuarantinedOperationIDs: []string{"op-canary"},
		CursorSequence: 42, CursorEpoch: 1, RetryCount: 2, RetryIn: 5 * time.Second,
		TransportProtocol: "https", TransportSecurityMode: "pinned", TransportURL: "https://home.example:8443",
		MigrationVersion: 9,
	}

	bundle, err := CopyDiagnostics(summary, technical)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"1.2.3", "windows", "true", "2026-01-02T03:04:05Z", "7", "offline", "healthy",
		"2026-01-01T00:00:00Z", "external drive", "1024", "ok", "up_to_date",
		"ws-canary", "device-canary", "transient_transport", "op-canary", "42", "1", "2", "5s",
		"https", "pinned", "https://home.example:8443", "9",
	} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("CopyDiagnostics() = %q, missing expected field value %q", bundle, want)
		}
	}
}

// TestCopyDiagnosticsOmitsNoteDataOnAZeroValueSummary proves the bundle
// never fabricates note-shaped content: a zero-value summary/technical
// pair (the state before any real collection populates them) renders only
// the allowlisted labels, never a stray title, query, or content field.
func TestCopyDiagnosticsOmitsNoteDataOnAZeroValueSummary(t *testing.T) {
	bundle, err := CopyDiagnostics(presentation.DiagnosticSummary{}, presentation.TechnicalDiagnostics{})
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{"title", "content", "query", "password", "token", "secret", "invite", "clipboard"} {
		if strings.Contains(strings.ToLower(bundle), prohibited) {
			t.Fatalf("CopyDiagnostics() = %q, must not contain %q", bundle, prohibited)
		}
	}
}

// forbiddenFieldSample carries a field name that must never survive into a
// diagnostics bundle, so checkNoForbiddenFields can be proven to actually
// catch it rather than only ever seeing the two real, already-clean
// schemas.
type forbiddenFieldSample struct {
	NotePassword string
}

// TestCheckNoForbiddenFieldsCatchesASecretLookingFieldName covers task
// 4.1's defense-in-depth guard: CopyDiagnostics only ever accepts the two
// fixed DiagnosticSummary/TechnicalDiagnostics schemas today, so this
// seeds the one condition that can't otherwise be exercised against
// them - a future field whose name alone should have blocked it from ever
// being added to a diagnostics struct.
func TestCheckNoForbiddenFieldsCatchesASecretLookingFieldName(t *testing.T) {
	err := checkNoForbiddenFields(reflect.TypeOf(forbiddenFieldSample{}))
	if err == nil || !strings.Contains(err.Error(), "NotePassword") {
		t.Fatalf("checkNoForbiddenFields() = %v, want an error naming NotePassword", err)
	}
}

// TestCheckNoForbiddenFieldsAcceptsTheRealDiagnosticsSchemas is a
// regression guard the other direction: the two schemas CopyDiagnostics
// actually ships today must keep passing, so a future contributor who
// widens forbiddenFieldSubstrings finds out immediately if it now also
// rejects a legitimate existing field.
func TestCheckNoForbiddenFieldsAcceptsTheRealDiagnosticsSchemas(t *testing.T) {
	if err := checkNoForbiddenFields(reflect.TypeOf(presentation.DiagnosticSummary{})); err != nil {
		t.Fatalf("DiagnosticSummary: %v", err)
	}
	if err := checkNoForbiddenFields(reflect.TypeOf(presentation.TechnicalDiagnostics{})); err != nil {
		t.Fatalf("TechnicalDiagnostics: %v", err)
	}
}

package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
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

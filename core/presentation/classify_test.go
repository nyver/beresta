package presentation

import (
	"testing"

	"github.com/beresta-app/beresta/core/transport"
)

func TestClassifyTransportError(t *testing.T) {
	tests := []struct {
		name  string
		class transport.ErrorClass
		want  UserErrorCategory
	}{
		{"no network", transport.ErrorClassNoNetwork, ErrorCategoryNoNetwork},
		{"server unavailable", transport.ErrorClassServerUnavailable, ErrorCategoryServerUnavailable},
		{"certificate mismatch", transport.ErrorClassCertificateMismatch, ErrorCategoryTrustOrConfiguration},
		{"authentication failed", transport.ErrorClassAuthenticationFailed, ErrorCategoryTrustOrConfiguration},
		{"unknown class", transport.ErrorClass("unknown"), ErrorCategoryServerUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyTransportError(tt.class, true)
			if got.Category != tt.want {
				t.Fatalf("Category = %q, want %q", got.Category, tt.want)
			}
			if !got.Category.Valid() {
				t.Fatalf("Category = %q is not a valid UserErrorCategory", got.Category)
			}
			if got.PrimaryAction != PrimaryRecoveryAction(tt.want) {
				t.Fatalf("PrimaryAction = %q, want %q", got.PrimaryAction, PrimaryRecoveryAction(tt.want))
			}
		})
	}
}

func TestClassifyTransportErrorPropagatesLocalDataSaved(t *testing.T) {
	saved := ClassifyTransportError(transport.ErrorClassNoNetwork, true)
	if !saved.LocalDataSaved {
		t.Fatal("LocalDataSaved = false, want true")
	}
	notSaved := ClassifyTransportError(transport.ErrorClassNoNetwork, false)
	if notSaved.LocalDataSaved {
		t.Fatal("LocalDataSaved = true, want false")
	}
}

func TestClassifyTransportErrorNetworkAndServerRetryAutomatically(t *testing.T) {
	for _, class := range []transport.ErrorClass{transport.ErrorClassNoNetwork, transport.ErrorClassServerUnavailable} {
		got := ClassifyTransportError(class, true)
		if !got.AutomaticRetry || got.RequiresUserAction {
			t.Fatalf("class %q: got %+v, want automatic retry without required user action", class, got)
		}
	}
}

func TestClassifyTransportErrorTrustFailuresRequireUserAction(t *testing.T) {
	for _, class := range []transport.ErrorClass{transport.ErrorClassCertificateMismatch, transport.ErrorClassAuthenticationFailed} {
		got := ClassifyTransportError(class, true)
		if got.AutomaticRetry || !got.RequiresUserAction {
			t.Fatalf("class %q: got %+v, want no automatic retry and required user action", class, got)
		}
	}
}

func TestClassifyQuarantine(t *testing.T) {
	got := ClassifyQuarantine(true)
	if got.Category != ErrorCategoryUnsafeIncomingChange {
		t.Fatalf("Category = %q, want %q", got.Category, ErrorCategoryUnsafeIncomingChange)
	}
	if got.PrimaryAction != RecoveryActionReviewUnsafeChange {
		t.Fatalf("PrimaryAction = %q, want %q", got.PrimaryAction, RecoveryActionReviewUnsafeChange)
	}
	if got.AutomaticRetry {
		t.Fatal("AutomaticRetry = true, want false: a quarantined operation never retries or applies on its own")
	}
	if !got.RequiresUserAction {
		t.Fatal("RequiresUserAction = false, want true")
	}
}

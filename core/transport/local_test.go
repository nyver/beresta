package transport_test

import (
	"context"
	"testing"

	"github.com/beresta-app/beresta/core/transport"
)

func TestLocalStatusIsDisabled(t *testing.T) {
	local := transport.NewLocal()

	got := local.Status(context.Background())

	if got != transport.StatusDisabled {
		t.Fatalf("Status() = %q, want %q", got, transport.StatusDisabled)
	}
}

func TestLocalStatusIsAlwaysDisabled(t *testing.T) {
	local := transport.NewLocal()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A canceled context must not change the reported status: Local never
	// performs I/O, so it has nothing to fail.
	got := local.Status(ctx)

	if got != transport.StatusDisabled {
		t.Fatalf("Status() with canceled context = %q, want %q", got, transport.StatusDisabled)
	}
}

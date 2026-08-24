package transport

import (
	"context"
	"testing"
)

func TestHTTPSyncStatusTracksWholeCycle(t *testing.T) {
	client := &HTTP{status: StatusOffline}

	client.BeginSync()
	if got := client.Status(context.Background()); got != StatusActive {
		t.Fatalf("status after BeginSync = %q, want %q", got, StatusActive)
	}

	client.CompleteSync()
	if got := client.Status(context.Background()); got != StatusCurrent {
		t.Fatalf("status after CompleteSync = %q, want %q", got, StatusCurrent)
	}

	client.BeginSync()
	client.SyncOffline()
	if got := client.Status(context.Background()); got != StatusOffline {
		t.Fatalf("status after retryable sync failure = %q, want %q", got, StatusOffline)
	}

	client.BeginSync()
	client.SyncFailed()
	if got := client.Status(context.Background()); got != StatusFailed {
		t.Fatalf("status after permanent sync failure = %q, want %q", got, StatusFailed)
	}
}

package main

import "testing"

func TestRunRejectsIncompleteOrUnknownCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"apply"}, {"rollback"}} {
		if err := run(t.Context(), args); err == nil {
			t.Fatalf("run(%v) error = nil", args)
		}
	}
}

func TestReleasePublicKeyFailsClosedWhenNotConfigured(t *testing.T) {
	previous := releasePublicKeyBase64
	releasePublicKeyBase64 = ""
	t.Cleanup(func() { releasePublicKeyBase64 = previous })
	if _, err := releasePublicKey(); err == nil {
		t.Fatal("releasePublicKey() error = nil")
	}
}

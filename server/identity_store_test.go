package server

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"
)

// TestVerifyChallengeBackfillsAnUnrecordedPlatform proves that a device
// registered without a platform hint (an older client, or a server that
// predates the "understandable device inventory" feature) picks one up from
// the very next successful challenge verification, without needing to be
// re-registered - see verifyChallenge's backfill UPDATE.
func TestVerifyChallengeBackfillsAnUnrecordedPlatform(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")

	devices, err := runtime.Storage.ListDevices(context.Background(), actor.Principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Platform != "" {
		t.Fatalf("devices = %+v, want exactly one device with no platform recorded yet", devices)
	}

	verifyWithPlatform(t, runtime, actor, "windows")

	devices, err = runtime.Storage.ListDevices(context.Background(), actor.Principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Platform != "windows" {
		t.Fatalf("devices = %+v, want the platform backfilled to windows", devices)
	}

	// A later proof reporting a different platform must never overwrite an
	// already-recorded value - the hint only fills a gap, it never corrects
	// or replaces a value the device already reported.
	verifyWithPlatform(t, runtime, actor, "android")

	devices, err = runtime.Storage.ListDevices(context.Background(), actor.Principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Platform != "windows" {
		t.Fatalf("devices = %+v, want the original platform preserved", devices)
	}
}

// TestVerifyChallengeDropsAnOversizedPlatformHintInsteadOfFailingAuth proves
// that a malformed platform hint (well past what any real client sends)
// never blocks the login/refresh it rides along on: Platform is optional
// display metadata, not an authentication input.
func TestVerifyChallengeDropsAnOversizedPlatformHintInsteadOfFailingAuth(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")

	verifyWithPlatform(t, runtime, actor, strings.Repeat("x", 33))

	devices, err := runtime.Storage.ListDevices(context.Background(), actor.Principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Platform != "" {
		t.Fatalf("devices = %+v, want the oversized hint dropped rather than stored", devices)
	}
}

func verifyWithPlatform(t *testing.T, runtime *Runtime, actor testActor, platform string) {
	t.Helper()
	challenge, err := runtime.Storage.IssueChallenge(context.Background(), actor.DeviceID, "sync", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	proof := ChallengeProof{
		ChallengeID: challenge.ID, DeviceID: actor.DeviceID, ServerFingerprint: challenge.ServerFingerprint,
		Nonce: challenge.Nonce, Scope: challenge.Scope, Platform: platform,
	}
	proof.Signature = ed25519.Sign(actor.PrivateKey, authSignatureInput(proof))
	if _, err := runtime.Storage.VerifyChallenge(context.Background(), proof, time.Now()); err != nil {
		t.Fatal(err)
	}
}

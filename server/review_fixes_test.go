package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPubSubEnforcesPerUserSubscriptionQuota(t *testing.T) {
	pubsub := NewPubSub(256)
	var held []Subscription
	for index := 0; index < maxSubscriptionsPerUser; index++ {
		subscription, err := pubsub.Subscribe("workspace", "greedy")
		if err != nil {
			t.Fatalf("subscription %d rejected: %v", index, err)
		}
		held = append(held, subscription)
	}
	if _, err := pubsub.Subscribe("workspace", "greedy"); err != ErrRateLimited {
		t.Fatalf("quota not enforced: err = %v", err)
	}
	// A different user must still be admitted: the global budget is untouched.
	other, err := pubsub.Subscribe("workspace", "polite")
	if err != nil {
		t.Fatalf("unrelated user rejected: %v", err)
	}
	other.Close()

	held[0].Close()
	replacement, err := pubsub.Subscribe("workspace", "greedy")
	if err != nil {
		t.Fatalf("released slot was not reusable: %v", err)
	}
	replacement.Close()
	for _, subscription := range held[1:] {
		subscription.Close()
	}
	if count := pubsub.subscriberCount(); count != 0 {
		t.Fatalf("subscriber count = %d, want 0", count)
	}
}

func TestPurgeExpiredAuthRecordsRemovesOnlyExpiredRows(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "purge")
	ctx := context.Background()
	now := time.Now()

	// A live session and a live challenge, plus one of each already expired.
	if _, err := runtime.Storage.IssueChallenge(ctx, actor.DeviceID, "sync", now); err != nil {
		t.Fatal(err)
	}
	stale := now.Add(-2 * runtime.Config.Auth.ChallengeTTL.Value())
	if _, err := runtime.Storage.IssueChallenge(ctx, actor.DeviceID, "sync", stale); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Storage.db.ExecContext(ctx,
		`INSERT INTO sessions(token_hash, user_id, device_id, scope, expires_at, created_at) VALUES (?, ?, ?, 'sync', ?, ?)`,
		bytes.Repeat([]byte{9}, 32), actor.UserID, actor.DeviceID, unixNow(now.Add(-time.Hour)), unixNow(stale)); err != nil {
		t.Fatal(err)
	}

	if err := runtime.Storage.PurgeExpiredAuthRecords(ctx, now); err != nil {
		t.Fatal(err)
	}

	var challenges, sessions int
	if err := runtime.Storage.db.QueryRowContext(ctx, `SELECT count(*) FROM challenges`).Scan(&challenges); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Storage.db.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	// Only the unconsumed, unexpired challenge survives: the actor's
	// registration challenge is consumed and the seeded one has expired.
	if challenges != 1 {
		t.Fatalf("challenges remaining = %d, want 1", challenges)
	}
	// The actor's registration session survives; only the seeded expired one goes.
	if sessions != 1 {
		t.Fatalf("sessions remaining = %d, want 1", sessions)
	}
	// The surviving session must still authenticate.
	if _, err := runtime.Storage.Authenticate(ctx, actor.Session.Token, now); err != nil {
		t.Fatalf("live session was purged: %v", err)
	}
}

func TestConfigAcceptsKeepDailyRangeAndRejectsOutOfRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, keep := range []string{"1", "14", "365"} {
		if err := os.WriteFile(path, []byte("backups:\n  keep_daily: "+keep+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path, "")
		if err != nil {
			t.Fatalf("keep_daily %s rejected: %v", keep, err)
		}
		if cfg.Backups.KeepDaily != atoiForTest(t, keep) {
			t.Fatalf("keep_daily = %d, want %s", cfg.Backups.KeepDaily, keep)
		}
	}
	for _, keep := range []string{"0", "-1", "366"} {
		if err := os.WriteFile(path, []byte("backups:\n  keep_daily: "+keep+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path, ""); err == nil {
			t.Fatalf("keep_daily %s was accepted", keep)
		}
	}
}

func TestConfigRejectsProtocolFixedBlobChunkKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("limits:\n  blob_chunk_bytes: 4194304\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path, "")
	if err == nil {
		t.Fatal("removed blob_chunk_bytes key was accepted")
	}
	if !strings.Contains(err.Error(), "blob_chunk_bytes") {
		t.Fatalf("error does not name the offending key: %v", err)
	}
	// The value itself remains available to the blob handlers.
	cfg := DefaultConfig()
	if cfg.Limits.BlobChunkBytes != BlobChunkBytes {
		t.Fatalf("BlobChunkBytes = %d, want %d", cfg.Limits.BlobChunkBytes, BlobChunkBytes)
	}
}

func atoiForTest(t *testing.T, value string) int {
	t.Helper()
	result := 0
	for _, digit := range value {
		result = result*10 + int(digit-'0')
	}
	return result
}

func TestStreamDropsPeerThatStopsAnsweringPings(t *testing.T) {
	runtime := newTestRuntime(t)
	// Compress the keepalive so the drop happens within the test budget.
	runtime.API.pingInterval = 50 * time.Millisecond
	runtime.API.pongWait = 400 * time.Millisecond
	actor := registerTestActor(t, runtime, "Silent")

	server := httptest.NewTLSServer(runtime.API)
	defer server.Close()
	websocketURL := "wss" + strings.TrimPrefix(server.URL, "https") + "/v1/sync/stream?workspace_id=" + actor.WorkspaceID
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- httptest uses an ephemeral self-signed certificate.
	connection, _, err := dialer.Dial(websocketURL, http.Header{"Authorization": []string{"Bearer " + actor.Session.Token}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	// Suppress the automatic pong reply so the peer looks wedged: the TCP
	// connection stays open, but the application never answers.
	connection.SetPingHandler(func(string) error { return nil })
	go func() {
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for runtime.API.pubsub.subscriberCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if count := runtime.API.pubsub.subscriberCount(); count != 0 {
		t.Fatalf("unresponsive peer still holds %d subscription(s); read deadline is not enforced", count)
	}
}

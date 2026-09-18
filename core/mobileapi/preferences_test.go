package mobileapi

import (
	"encoding/hex"
	"path/filepath"
	"testing"
)

// TestPlanCacheEvictionProtectsPinnedAndUnsynchronizedThenEvictsOverBudget
// covers task 5.8's restart-safe cache-cleanup maintenance: unlike
// TestAttachmentEvictionPolicyProtectsOriginalsPinsAndSelections (which
// only exercises the pure shouldEvictCachedAttachment helper),this drives
// the real RecordCachedAttachment/PlanCacheEviction transaction pair, so it
// also proves the eviction plan's bookkeeping deletes commit atomically
// (a second call never re-reports what a prior call already evicted) and
// that pinned/unsynchronized-original rows are never candidates even when
// they are the ones keeping the account over its cache budget.
func TestPlanCacheEvictionProtectsPinnedAndUnsynchronizedThenEvictsOverBudget(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(service.Close)
	if _, err := service.CreateAccount("create", dbPath, "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := service.UpdateSettings("settings", `{"language":"en","auto_lock_minutes":5,"backup_destination":"","attachment_retention":"all","selected_notebooks":[],"cache_limit_bytes":100}`); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	// mobile_attachment_cache.blob_id references attachments(blob_id), so
	// each cache record needs a real attachment row behind it rather than
	// an arbitrary hex blob ID.
	noteJSON, err := service.CreateNote("create-note", "", "Has cached attachments")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	noteID := decodeJSON[map[string]any](t, noteJSON)["id"].(string)
	pinned := addTestAttachment(t, service, noteID, "pinned.bin")
	unsynchronized := addTestAttachment(t, service, noteID, "unsynchronized.bin")
	evictableA := addTestAttachment(t, service, noteID, "evictable-a.bin")
	evictableB := addTestAttachment(t, service, noteID, "evictable-b.bin")

	if err := service.RecordCachedAttachment("r1", pinned, "", 40, true, true); err != nil {
		t.Fatalf("RecordCachedAttachment (pinned): %v", err)
	}
	if err := service.RecordCachedAttachment("r2", unsynchronized, "", 40, false, false); err != nil {
		t.Fatalf("RecordCachedAttachment (unsynchronized original): %v", err)
	}
	if err := service.RecordCachedAttachment("r3", evictableA, "", 40, false, true); err != nil {
		t.Fatalf("RecordCachedAttachment (evictable A): %v", err)
	}
	if err := service.RecordCachedAttachment("r4", evictableB, "", 40, false, true); err != nil {
		t.Fatalf("RecordCachedAttachment (evictable B): %v", err)
	}

	evictedJSON, err := service.PlanCacheEviction("plan")
	if err != nil {
		t.Fatalf("PlanCacheEviction: %v", err)
	}
	evicted := decodeJSON[[]string](t, evictedJSON)
	evictedSet := map[string]bool{}
	for _, id := range evicted {
		evictedSet[id] = true
	}
	if evictedSet[pinned] {
		t.Fatalf("PlanCacheEviction evicted a pinned entry: %v", evicted)
	}
	if evictedSet[unsynchronized] {
		t.Fatalf("PlanCacheEviction evicted an unsynchronized-original entry: %v", evicted)
	}
	if !evictedSet[evictableA] || !evictedSet[evictableB] {
		t.Fatalf("PlanCacheEviction = %v, want both evictable entries over the 100-byte budget", evicted)
	}

	// The plan's own deletes must have committed, so a second call never
	// re-reports the same entries.
	againJSON, err := service.PlanCacheEviction("plan-again")
	if err != nil {
		t.Fatalf("PlanCacheEviction (second call): %v", err)
	}
	if again := decodeJSON[[]string](t, againJSON); len(again) != 0 {
		t.Fatalf("PlanCacheEviction second call = %v, want no entries left to evict", again)
	}
}

// addTestAttachment adds a real attachment to noteID and returns its blob
// ID hex, so callers exercising mobile_attachment_cache (whose blob_id
// column references attachments(blob_id)) have a row that satisfies the
// foreign key.
func addTestAttachment(t *testing.T, service *Service, noteID, displayName string) string {
	t.Helper()
	// Attachments are content-addressed and dedup by blob content within a
	// workspace (core/account.AddAttachment), so each one needs distinct
	// content to land as its own blob and note_attachments row.
	content := []byte("test attachment content: " + displayName)
	if err := service.AddAttachmentData("add-"+displayName, noteID, displayName, "application/octet-stream", content); err != nil {
		t.Fatalf("AddAttachmentData(%s): %v", displayName, err)
	}
	attachmentsJSON, err := service.ListNoteAttachments("list-"+displayName, noteID)
	if err != nil {
		t.Fatalf("ListNoteAttachments(%s): %v", displayName, err)
	}
	attachments := decodeJSON[[]map[string]any](t, attachmentsJSON)
	for _, a := range attachments {
		if a["display_name"] == displayName {
			return a["blob_id"].(string)
		}
	}
	t.Fatalf("attachment %q not found after AddAttachmentData", displayName)
	return ""
}

func TestAttachmentEvictionPolicyProtectsOriginalsPinsAndSelections(t *testing.T) {
	notebook := []byte{1, 2, 3}
	selected := map[string]struct{}{hex.EncodeToString(notebook): {}}
	tests := []struct {
		name         string
		mode         string
		limit, total int64
		pinned       bool
		synchronized bool
		notebook     []byte
		want         bool
	}{
		{name: "unsynchronized original", mode: retentionMetadata, synchronized: false},
		{name: "pinned copy", mode: retentionMetadata, pinned: true, synchronized: true},
		{name: "metadata only", mode: retentionMetadata, synchronized: true, want: true},
		{name: "selected notebook under limit", mode: retentionSelected, synchronized: true, notebook: notebook, limit: 10, total: 5},
		{name: "unselected notebook", mode: retentionSelected, synchronized: true, notebook: []byte{9}, limit: 10, total: 5, want: true},
		{name: "selected notebook over limit", mode: retentionSelected, synchronized: true, notebook: notebook, limit: 10, total: 11, want: true},
		{name: "all under limit", mode: retentionAll, synchronized: true, limit: 10, total: 5},
		{name: "all over limit", mode: retentionAll, synchronized: true, limit: 10, total: 11, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prefs := defaultMobilePreferences()
			prefs.AttachmentRetention, prefs.CacheLimitBytes = test.mode, test.limit
			if got := shouldEvictCachedAttachment(prefs, selected, test.notebook, test.pinned, test.synchronized, test.total); got != test.want {
				t.Fatalf("shouldEvictCachedAttachment() = %v, want %v", got, test.want)
			}
		})
	}
}

package mobileapi

import (
	"encoding/hex"
	"testing"
)

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

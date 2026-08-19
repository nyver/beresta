package main

import (
	"testing"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

func TestParseIDRoundTripsIDString(t *testing.T) {
	id, err := model.NewID()
	if err != nil {
		t.Fatalf("model.NewID: %v", err)
	}
	s := idString(id)
	got, err := parseID(s)
	if err != nil {
		t.Fatalf("parseID(%q): %v", s, err)
	}
	if got != id {
		t.Fatalf("parseID(idString(id)) = %v, want %v", got, id)
	}
}

func TestParseIDEmptyStringIsNil(t *testing.T) {
	got, err := parseID("")
	if err != nil {
		t.Fatalf("parseID(\"\"): %v", err)
	}
	if got != model.Nil {
		t.Fatalf("parseID(\"\") = %v, want model.Nil", got)
	}
	if idString(model.Nil) != "" {
		t.Fatalf("idString(model.Nil) = %q, want \"\"", idString(model.Nil))
	}
}

func TestParseIDRejectsGarbage(t *testing.T) {
	if _, err := parseID("not-a-uuid"); err == nil {
		t.Fatal("parseID(garbage) error = nil, want error")
	}
}

func TestBlobIDRoundTrip(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	blobID, err := store.ParseBlobID(raw)
	if err != nil {
		t.Fatalf("store.ParseBlobID: %v", err)
	}
	s := blobIDString(blobID)
	got, err := parseBlobID(s)
	if err != nil {
		t.Fatalf("parseBlobID(%q): %v", s, err)
	}
	if got != blobID {
		t.Fatalf("parseBlobID(blobIDString(id)) = %v, want %v", got, blobID)
	}
}

func TestParseBlobIDRejectsGarbage(t *testing.T) {
	if _, err := parseBlobID("zz"); err == nil {
		t.Fatal("parseBlobID(garbage) error = nil, want error")
	}
}

func TestDecodeBase64(t *testing.T) {
	got, err := decodeBase64("aGVsbG8=")
	if err != nil {
		t.Fatalf("decodeBase64: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("decodeBase64 = %q, want %q", got, "hello")
	}
	if _, err := decodeBase64("not base64!!"); err == nil {
		t.Fatal("decodeBase64(invalid) error = nil, want error")
	}
}

func TestParseYjsFormat(t *testing.T) {
	cases := map[string]yjsadapter.Format{"v1": yjsadapter.FormatV1, "v2": yjsadapter.FormatV2}
	for s, want := range cases {
		got, err := parseYjsFormat(s)
		if err != nil {
			t.Fatalf("parseYjsFormat(%q): %v", s, err)
		}
		if got != want {
			t.Fatalf("parseYjsFormat(%q) = %v, want %v", s, got, want)
		}
	}
	if _, err := parseYjsFormat("v3"); err == nil {
		t.Fatal("parseYjsFormat(\"v3\") error = nil, want error")
	}
}

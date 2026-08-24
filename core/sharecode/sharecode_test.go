package sharecode

import (
	"strings"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

func mustID(t *testing.T) model.ID {
	t.Helper()
	id, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIdentityCodeRoundTrip(t *testing.T) {
	userID := mustID(t)
	key := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	code, err := EncodeIdentity(userID, key)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, "beresta://identity?") {
		t.Fatalf("unexpected code shape: %q", code)
	}
	decodedID, decodedKey, err := DecodeIdentity(code)
	if err != nil {
		t.Fatal(err)
	}
	if decodedID != userID {
		t.Fatalf("user id mismatch: got %s want %s", decodedID, userID)
	}
	if string(decodedKey) != string(key) {
		t.Fatalf("identity key mismatch: got %x want %x", decodedKey, key)
	}
}

func TestDecodeIdentityRejectsMalformedInput(t *testing.T) {
	userID := mustID(t)
	validKey := []byte{1, 2, 3}
	valid, err := EncodeIdentity(userID, validKey)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"empty":            "",
		"wrong scheme":     "https://identity?user=" + userID.String() + "&key=010203",
		"wrong host":       "beresta://grant?user=" + userID.String() + "&key=010203",
		"missing user":     "beresta://identity?key=010203",
		"invalid user":     "beresta://identity?user=not-a-uuid&key=010203",
		"missing key":      "beresta://identity?user=" + userID.String(),
		"invalid key":      "beresta://identity?user=" + userID.String() + "&key=zz",
		"truncated":        valid[:len(valid)-4],
		"not a uri at all": "\x00\x01",
	}
	for name, code := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := DecodeIdentity(code); err == nil {
				t.Fatalf("expected DecodeIdentity to reject %q", code)
			}
		})
	}
}

func TestGrantCodeRoundTrip(t *testing.T) {
	workspaceID := mustID(t)
	keyID := []byte{9, 8, 7, 6}
	authority := []byte{1, 1, 1, 1, 1}
	signature := []byte{2, 2, 2, 2, 2, 2}
	code, err := EncodeGrant(workspaceID, keyID, authority, signature)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, "beresta://grant?") {
		t.Fatalf("unexpected code shape: %q", code)
	}
	decodedWorkspace, decodedKeyID, decodedAuthority, decodedSignature, err := DecodeGrant(code)
	if err != nil {
		t.Fatal(err)
	}
	if decodedWorkspace != workspaceID {
		t.Fatalf("workspace id mismatch: got %s want %s", decodedWorkspace, workspaceID)
	}
	if string(decodedKeyID) != string(keyID) {
		t.Fatalf("key id mismatch: got %x want %x", decodedKeyID, keyID)
	}
	if string(decodedAuthority) != string(authority) {
		t.Fatalf("authority key mismatch: got %x want %x", decodedAuthority, authority)
	}
	if string(decodedSignature) != string(signature) {
		t.Fatalf("signature mismatch: got %x want %x", decodedSignature, signature)
	}
}

func TestDecodeGrantRejectsMalformedInput(t *testing.T) {
	workspaceID := mustID(t)
	cases := map[string]string{
		"empty":             "",
		"wrong scheme":      "https://grant?workspace=" + workspaceID.String() + "&key=01&authority=02&sig=03",
		"wrong host":        "beresta://identity?workspace=" + workspaceID.String() + "&key=01&authority=02&sig=03",
		"missing workspace": "beresta://grant?key=01&authority=02&sig=03",
		"invalid workspace": "beresta://grant?workspace=not-a-uuid&key=01&authority=02&sig=03",
		"missing key":       "beresta://grant?workspace=" + workspaceID.String() + "&authority=02&sig=03",
		"invalid key":       "beresta://grant?workspace=" + workspaceID.String() + "&key=zz&authority=02&sig=03",
		"missing authority": "beresta://grant?workspace=" + workspaceID.String() + "&key=01&sig=03",
		"missing sig":       "beresta://grant?workspace=" + workspaceID.String() + "&key=01&authority=02",
	}
	for name, code := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, _, _, err := DecodeGrant(code); err == nil {
				t.Fatalf("expected DecodeGrant to reject %q", code)
			}
		})
	}
}

func TestEncodeIdentityRejectsInvalidInput(t *testing.T) {
	if _, err := EncodeIdentity(model.Nil, []byte{1}); err == nil {
		t.Fatal("expected EncodeIdentity to reject a nil user id")
	}
	userID := mustID(t)
	if _, err := EncodeIdentity(userID, nil); err == nil {
		t.Fatal("expected EncodeIdentity to reject an empty identity key")
	}
}

func TestEncodeGrantRejectsInvalidInput(t *testing.T) {
	workspaceID := mustID(t)
	nonEmpty := []byte{1}
	if _, err := EncodeGrant(model.Nil, nonEmpty, nonEmpty, nonEmpty); err == nil {
		t.Fatal("expected EncodeGrant to reject a nil workspace id")
	}
	if _, err := EncodeGrant(workspaceID, nil, nonEmpty, nonEmpty); err == nil {
		t.Fatal("expected EncodeGrant to reject an empty key id")
	}
	if _, err := EncodeGrant(workspaceID, nonEmpty, nil, nonEmpty); err == nil {
		t.Fatal("expected EncodeGrant to reject an empty authority key")
	}
	if _, err := EncodeGrant(workspaceID, nonEmpty, nonEmpty, nil); err == nil {
		t.Fatal("expected EncodeGrant to reject an empty signature")
	}
}

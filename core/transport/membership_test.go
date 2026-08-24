package transport

import "testing"

func TestFindKeyEnvelope(t *testing.T) {
	envelopes := []RemoteKeyEnvelope{
		{KeyID: "aabb", Envelope: []byte("first")},
		{KeyID: "ccdd", Envelope: []byte("second")},
	}
	envelope, found := FindKeyEnvelope(envelopes, []byte{0xcc, 0xdd})
	if !found || string(envelope) != "second" {
		t.Fatalf("FindKeyEnvelope = (%q, %v), want (\"second\", true)", envelope, found)
	}
}

func TestFindKeyEnvelopeMissing(t *testing.T) {
	envelopes := []RemoteKeyEnvelope{{KeyID: "aabb", Envelope: []byte("first")}}
	if _, found := FindKeyEnvelope(envelopes, []byte{0x99}); found {
		t.Fatal("FindKeyEnvelope reported a match for a key ID that is not present")
	}
}

func TestFindKeyEnvelopeToleratesMalformedServerKeyID(t *testing.T) {
	envelopes := []RemoteKeyEnvelope{{KeyID: "not-hex", Envelope: []byte("first")}}
	if _, found := FindKeyEnvelope(envelopes, []byte{0x01}); found {
		t.Fatal("FindKeyEnvelope matched a malformed server key_id instead of treating it as a non-match")
	}
}

func TestSelfRole(t *testing.T) {
	members := []RemoteMember{
		{UserID: "alice", Role: "owner"},
		{UserID: "bob", Role: "member"},
	}
	if role := SelfRole(members, "bob"); role != "member" {
		t.Fatalf("SelfRole(bob) = %q, want member", role)
	}
	if role := SelfRole(members, "alice"); role != "owner" {
		t.Fatalf("SelfRole(alice) = %q, want owner", role)
	}
}

func TestSelfRoleNotAMember(t *testing.T) {
	members := []RemoteMember{{UserID: "alice", Role: "owner"}}
	if role := SelfRole(members, "carol"); role != "unknown" {
		t.Fatalf("SelfRole(carol) = %q, want unknown", role)
	}
}

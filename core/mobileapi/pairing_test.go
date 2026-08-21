package mobileapi

import (
	"bytes"
	"testing"
	"time"
)

func TestSPAKE2PairingConfirmationAndEncryptedFrame(t *testing.T) {
	initiator, err := NewPairingSession("initiator", "483921")
	if err != nil {
		t.Fatal(err)
	}
	defer initiator.Close()
	responder, err := NewPairingSession("responder", "483921")
	if err != nil {
		t.Fatal(err)
	}
	defer responder.Close()
	initiatorProof, err := initiator.Finish(responder.PublicMessage())
	if err != nil {
		t.Fatal(err)
	}
	responderProof, err := responder.Finish(initiator.PublicMessage())
	if err != nil {
		t.Fatal(err)
	}
	if !initiator.VerifyConfirmation(responderProof) || !responder.VerifyConfirmation(initiatorProof) {
		t.Fatal("matching short codes did not confirm")
	}
	want := []byte("opaque snapshot and operation bootstrap")
	sealed, err := initiator.Seal(want)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := responder.Open(sealed)
	if err != nil || !bytes.Equal(opened, want) {
		t.Fatalf("paired frame = %q, %v", opened, err)
	}
	if _, err := responder.Open(sealed); err == nil {
		t.Fatal("paired frame replay was accepted")
	}
}

func TestSPAKE2PairingExpiresBeforeBootstrap(t *testing.T) {
	session, err := NewPairingSession("initiator", "483921")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	session.expires = time.Now().Add(-time.Second)
	if _, err := session.Finish(session.PublicMessage()); err == nil {
		t.Fatal("expired pairing session accepted a peer")
	}
}

func TestSPAKE2PairingMismatchAbortsBeforeData(t *testing.T) {
	initiator, _ := NewPairingSession("initiator", "111111")
	responder, _ := NewPairingSession("responder", "222222")
	defer initiator.Close()
	defer responder.Close()
	initiatorProof, err := initiator.Finish(responder.PublicMessage())
	if err != nil {
		t.Fatal(err)
	}
	responderProof, err := responder.Finish(initiator.PublicMessage())
	if err != nil {
		t.Fatal(err)
	}
	if initiator.VerifyConfirmation(responderProof) || responder.VerifyConfirmation(initiatorProof) {
		t.Fatal("mismatched short codes confirmed")
	}
	if _, err := initiator.Seal([]byte("must remain unavailable")); err == nil {
		t.Fatal("mismatched session accepted a data frame")
	}
}

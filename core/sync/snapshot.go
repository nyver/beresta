package sync

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
)

type Snapshot struct {
	ID              model.ID
	WorkspaceID     model.ID
	BaseSequence    uint64
	CursorEpoch     uint32
	KeyID           []byte
	CreatorDeviceID model.ID
	Clock           model.HLC
	Nonce           []byte
	CiphertextHash  []byte
	Ciphertext      []byte
	Signature       []byte
}

type SnapshotAcknowledgement struct {
	SnapshotID     model.ID
	WorkspaceID    model.ID
	DeviceID       model.ID
	BaseSequence   uint64
	CiphertextHash []byte
	Signature      []byte
}

func SnapshotSignatureInput(snapshot Snapshot) ([]byte, error) {
	digest := sha256.Sum256(snapshot.Ciphertext)
	if len(snapshot.CiphertextHash) != sha256.Size || !constantBytesEqual(snapshot.CiphertextHash, digest[:]) {
		return nil, errors.New("sync: snapshot ciphertext hash mismatch")
	}
	return corecrypto.CanonicalSnapshotSignatureInput(corecrypto.SnapshotSignatureFields{
		SnapshotID: snapshot.ID.Bytes(), WorkspaceID: snapshot.WorkspaceID.Bytes(), BaseSequence: snapshot.BaseSequence,
		CursorEpoch: snapshot.CursorEpoch, KeyID: snapshot.KeyID, CreatorDeviceID: snapshot.CreatorDeviceID.Bytes(),
		HLCPhysicalMS: snapshot.Clock.PhysicalMS, HLCLogical: snapshot.Clock.Logical, HLCDeviceID: snapshot.Clock.DeviceID.Bytes(),
		Nonce: snapshot.Nonce, CiphertextHash: snapshot.CiphertextHash, Ciphertext: snapshot.Ciphertext,
	})
}

func SignSnapshot(snapshot *Snapshot, private *corecrypto.Secret) error {
	if snapshot == nil {
		return errors.New("sync: nil snapshot")
	}
	input, err := SnapshotSignatureInput(*snapshot)
	if err != nil {
		return err
	}
	snapshot.Signature, err = corecrypto.SignCanonical(corecrypto.CryptoProfileV1, private, corecrypto.SignatureDomainSnapshot, input)
	return err
}

func VerifySnapshot(snapshot Snapshot, publicKey []byte) error {
	if len(snapshot.Signature) != ed25519.SignatureSize {
		return errors.New("sync: invalid snapshot signature")
	}
	input, err := SnapshotSignatureInput(snapshot)
	if err != nil {
		return err
	}
	return corecrypto.VerifyCanonical(corecrypto.CryptoProfileV1, publicKey, corecrypto.SignatureDomainSnapshot, input, snapshot.Signature)
}

func SnapshotAcknowledgementInput(ack SnapshotAcknowledgement) ([]byte, error) {
	return corecrypto.CanonicalSnapshotAckSignatureInput(corecrypto.SnapshotAckSignatureFields{
		SnapshotID: ack.SnapshotID.Bytes(), WorkspaceID: ack.WorkspaceID.Bytes(), DeviceID: ack.DeviceID.Bytes(),
		BaseSequence: ack.BaseSequence, CiphertextHash: ack.CiphertextHash,
	})
}

func SignSnapshotAcknowledgement(ack *SnapshotAcknowledgement, private *corecrypto.Secret) error {
	if ack == nil {
		return errors.New("sync: nil snapshot acknowledgement")
	}
	input, err := SnapshotAcknowledgementInput(*ack)
	if err != nil {
		return err
	}
	ack.Signature, err = corecrypto.SignCanonical(corecrypto.CryptoProfileV1, private, corecrypto.SignatureDomainSnapshotAck, input)
	return err
}

func VerifySnapshotAcknowledgement(ack SnapshotAcknowledgement, publicKey []byte) error {
	if len(ack.Signature) != ed25519.SignatureSize {
		return errors.New("sync: invalid snapshot acknowledgement signature")
	}
	input, err := SnapshotAcknowledgementInput(ack)
	if err != nil {
		return err
	}
	if err := corecrypto.VerifyCanonical(corecrypto.CryptoProfileV1, publicKey, corecrypto.SignatureDomainSnapshotAck, input, ack.Signature); err != nil {
		return fmt.Errorf("sync: verify snapshot acknowledgement: %w", err)
	}
	return nil
}

func constantBytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

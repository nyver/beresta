package crypto

import "errors"

const MaxSnapshotCiphertextBytes = 256 << 20

var (
	ErrInvalidSnapshotFields    = errors.New("crypto: invalid snapshot signature fields")
	ErrInvalidSnapshotAckFields = errors.New("crypto: invalid snapshot acknowledgement fields")
)

// SnapshotSignatureFields is the closed signed portion of
// beresta.workspace-snapshot.v1. The server eligibility timestamp is excluded.
type SnapshotSignatureFields struct {
	SnapshotID      []byte
	WorkspaceID     []byte
	BaseSequence    uint64
	CursorEpoch     uint32
	KeyID           []byte
	CreatorDeviceID []byte
	HLCPhysicalMS   uint64
	HLCLogical      uint32
	HLCDeviceID     []byte
	Nonce           []byte
	CiphertextHash  []byte
	Ciphertext      []byte
}

// CanonicalSnapshotSignatureInput encodes the signed snapshot map using the
// same deterministic CBOR profile as the other v1 cryptographic structures.
func CanonicalSnapshotSignatureInput(fields SnapshotSignatureFields) ([]byte, error) {
	if len(fields.SnapshotID) != SnapshotIDBytes || len(fields.WorkspaceID) != WorkspaceIDBytes ||
		len(fields.KeyID) != KeyIDBytes || len(fields.CreatorDeviceID) != DeviceIDBytes ||
		len(fields.HLCDeviceID) != DeviceIDBytes || string(fields.HLCDeviceID) != string(fields.CreatorDeviceID) ||
		fields.CursorEpoch == 0 || len(fields.Nonce) != XChaCha20NonceBytes || len(fields.CiphertextHash) != 32 ||
		len(fields.Ciphertext) < AEADTagBytes || len(fields.Ciphertext) > MaxSnapshotCiphertextBytes {
		return nil, ErrInvalidSnapshotFields
	}
	encoded := make([]byte, 0, 512)
	encoded = appendCBORHeader(encoded, 5, 12)
	encoded = appendCBORText(encoded, "protocol")
	encoded = appendCBORText(encoded, SyncProtocolV1)
	encoded = appendCBORText(encoded, "schema_version")
	encoded = appendCBORUint(encoded, SchemaVersionV1)
	encoded = appendCBORText(encoded, "snapshot_id")
	encoded = appendCBORBytes(encoded, fields.SnapshotID)
	encoded = appendCBORText(encoded, "workspace_id")
	encoded = appendCBORBytes(encoded, fields.WorkspaceID)
	encoded = appendCBORText(encoded, "base_seq")
	encoded = appendCBORUint(encoded, fields.BaseSequence)
	encoded = appendCBORText(encoded, "cursor_epoch")
	encoded = appendCBORUint(encoded, uint64(fields.CursorEpoch))
	encoded = appendCBORText(encoded, "key_id")
	encoded = appendCBORBytes(encoded, fields.KeyID)
	encoded = appendCBORText(encoded, "creator_device_id")
	encoded = appendCBORBytes(encoded, fields.CreatorDeviceID)
	encoded = appendCBORText(encoded, "created_hlc")
	encoded = appendCBORHeader(encoded, 5, 3)
	encoded = appendCBORText(encoded, "physical_ms")
	encoded = appendCBORUint(encoded, fields.HLCPhysicalMS)
	encoded = appendCBORText(encoded, "logical")
	encoded = appendCBORUint(encoded, uint64(fields.HLCLogical))
	encoded = appendCBORText(encoded, "device_id")
	encoded = appendCBORBytes(encoded, fields.HLCDeviceID)
	encoded = appendCBORText(encoded, "nonce")
	encoded = appendCBORBytes(encoded, fields.Nonce)
	encoded = appendCBORText(encoded, "ciphertext_hash")
	encoded = appendCBORBytes(encoded, fields.CiphertextHash)
	encoded = appendCBORText(encoded, "ciphertext")
	encoded = appendCBORBytes(encoded, fields.Ciphertext)
	return encoded, nil
}

type SnapshotAckSignatureFields struct {
	SnapshotID     []byte
	WorkspaceID    []byte
	DeviceID       []byte
	BaseSequence   uint64
	CiphertextHash []byte
}

// CanonicalSnapshotAckSignatureInput encodes beresta.snapshot-ack.v1 without
// its signature field.
func CanonicalSnapshotAckSignatureInput(fields SnapshotAckSignatureFields) ([]byte, error) {
	if len(fields.SnapshotID) != SnapshotIDBytes || len(fields.WorkspaceID) != WorkspaceIDBytes ||
		len(fields.DeviceID) != DeviceIDBytes || len(fields.CiphertextHash) != 32 {
		return nil, ErrInvalidSnapshotAckFields
	}
	encoded := make([]byte, 0, 192)
	encoded = appendCBORHeader(encoded, 5, 7)
	encoded = appendCBORText(encoded, "protocol")
	encoded = appendCBORText(encoded, SyncProtocolV1)
	encoded = appendCBORText(encoded, "schema_version")
	encoded = appendCBORUint(encoded, SchemaVersionV1)
	encoded = appendCBORText(encoded, "snapshot_id")
	encoded = appendCBORBytes(encoded, fields.SnapshotID)
	encoded = appendCBORText(encoded, "workspace_id")
	encoded = appendCBORBytes(encoded, fields.WorkspaceID)
	encoded = appendCBORText(encoded, "device_id")
	encoded = appendCBORBytes(encoded, fields.DeviceID)
	encoded = appendCBORText(encoded, "base_seq")
	encoded = appendCBORUint(encoded, fields.BaseSequence)
	encoded = appendCBORText(encoded, "ciphertext_hash")
	encoded = appendCBORBytes(encoded, fields.CiphertextHash)
	return encoded, nil
}

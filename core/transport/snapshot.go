package transport

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
)

type snapshotJSON struct {
	Protocol        string     `json:"protocol"`
	SchemaVersion   int        `json:"schema_version"`
	ID              string     `json:"snapshot_id"`
	WorkspaceID     string     `json:"workspace_id"`
	BaseSequence    int64      `json:"base_seq"`
	CursorEpoch     int64      `json:"cursor_epoch"`
	KeyID           string     `json:"key_id"`
	CreatorDeviceID string     `json:"creator_device_id"`
	HLCPhysicalMS   int64      `json:"hlc_physical_ms"`
	HLCLogical      uint32     `json:"hlc_logical"`
	Nonce           []byte     `json:"nonce"`
	CiphertextHash  []byte     `json:"ciphertext_hash"`
	Ciphertext      []byte     `json:"ciphertext,omitempty"`
	Signature       []byte     `json:"sig"`
	EligibleAt      *time.Time `json:"eligible_at,omitempty"`
}

type snapshotAckJSON struct {
	Protocol       string `json:"protocol"`
	SchemaVersion  int    `json:"schema_version"`
	SnapshotID     string `json:"snapshot_id"`
	WorkspaceID    string `json:"workspace_id"`
	DeviceID       string `json:"device_id"`
	BaseSequence   int64  `json:"base_seq"`
	CiphertextHash []byte `json:"ciphertext_hash"`
	Signature      []byte `json:"sig"`
}

func (h *HTTP) PutSnapshot(ctx context.Context, snapshot coresync.Snapshot) error {
	if _, err := coresync.SnapshotSignatureInput(snapshot); err != nil {
		return err
	}
	var response struct {
		SnapshotID string `json:"snapshot_id"`
	}
	if err := h.doJSON(ctx, http.MethodPost, "/v1/snapshots", snapshotFromWire(snapshot), &response, true); err != nil {
		return err
	}
	if response.SnapshotID != snapshot.ID.String() {
		return errors.New("transport: snapshot response identifier mismatch")
	}
	return nil
}

func (h *HTTP) LatestSnapshot(ctx context.Context, workspaceID model.ID) (coresync.Snapshot, error) {
	path := "/v1/snapshots/latest?" + url.Values{"workspace_id": {workspaceID.String()}}.Encode()
	var response snapshotJSON
	if err := h.doJSON(ctx, http.MethodGet, path, nil, &response, true); err != nil {
		return coresync.Snapshot{}, err
	}
	result, err := response.toWire()
	if err != nil {
		return coresync.Snapshot{}, err
	}
	if result.WorkspaceID != workspaceID {
		return coresync.Snapshot{}, errors.New("transport: snapshot workspace mismatch")
	}
	return result, nil
}

func (h *HTTP) AcknowledgeSnapshot(ctx context.Context, ack coresync.SnapshotAcknowledgement) (bool, error) {
	if _, err := coresync.SnapshotAcknowledgementInput(ack); err != nil {
		return false, err
	}
	request := snapshotAckJSON{
		Protocol: coresync.ProtocolV1, SchemaVersion: int(coresync.SchemaVersionV1), SnapshotID: ack.SnapshotID.String(),
		WorkspaceID: ack.WorkspaceID.String(), DeviceID: ack.DeviceID.String(), BaseSequence: int64(ack.BaseSequence),
		CiphertextHash: ack.CiphertextHash, Signature: ack.Signature,
	}
	var response struct {
		Eligible bool `json:"eligible_for_compaction"`
	}
	err := h.doJSON(ctx, http.MethodPost, "/v1/snapshots/"+ack.SnapshotID.String()+"/ack", request, &response, true)
	return response.Eligible, err
}

func snapshotFromWire(snapshot coresync.Snapshot) snapshotJSON {
	return snapshotJSON{
		Protocol: coresync.ProtocolV1, SchemaVersion: int(coresync.SchemaVersionV1), ID: snapshot.ID.String(),
		WorkspaceID: snapshot.WorkspaceID.String(), BaseSequence: int64(snapshot.BaseSequence), CursorEpoch: int64(snapshot.CursorEpoch),
		KeyID: hex.EncodeToString(snapshot.KeyID), CreatorDeviceID: snapshot.CreatorDeviceID.String(),
		HLCPhysicalMS: int64(snapshot.Clock.PhysicalMS), HLCLogical: snapshot.Clock.Logical, Nonce: snapshot.Nonce,
		CiphertextHash: snapshot.CiphertextHash, Ciphertext: snapshot.Ciphertext, Signature: snapshot.Signature,
	}
}

func (snapshot snapshotJSON) toWire() (coresync.Snapshot, error) {
	if snapshot.Protocol != coresync.ProtocolV1 || snapshot.SchemaVersion != int(coresync.SchemaVersionV1) || snapshot.BaseSequence < 0 || snapshot.CursorEpoch <= 0 || snapshot.CursorEpoch > math.MaxUint32 || snapshot.HLCPhysicalMS < 0 {
		return coresync.Snapshot{}, coresync.ErrUnsupportedVersion
	}
	id, err := parseCanonicalID(snapshot.ID)
	if err != nil {
		return coresync.Snapshot{}, err
	}
	workspaceID, err := parseCanonicalID(snapshot.WorkspaceID)
	if err != nil {
		return coresync.Snapshot{}, err
	}
	deviceID, err := parseCanonicalID(snapshot.CreatorDeviceID)
	if err != nil {
		return coresync.Snapshot{}, err
	}
	keyID, err := hex.DecodeString(snapshot.KeyID)
	if err != nil || len(keyID) != 16 || hex.EncodeToString(keyID) != snapshot.KeyID {
		return coresync.Snapshot{}, errors.New("transport: malformed snapshot key identifier")
	}
	result := coresync.Snapshot{
		ID: id, WorkspaceID: workspaceID, BaseSequence: uint64(snapshot.BaseSequence), CursorEpoch: uint32(snapshot.CursorEpoch), KeyID: keyID,
		CreatorDeviceID: deviceID, Clock: model.HLC{PhysicalMS: uint64(snapshot.HLCPhysicalMS), Logical: snapshot.HLCLogical, DeviceID: deviceID},
		Nonce: snapshot.Nonce, CiphertextHash: snapshot.CiphertextHash, Ciphertext: snapshot.Ciphertext, Signature: snapshot.Signature,
	}
	if _, err := coresync.SnapshotSignatureInput(result); err != nil {
		return coresync.Snapshot{}, err
	}
	return result, nil
}

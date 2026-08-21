package account

import (
	"bytes"
	"testing"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
)

func TestSnapshotOperationArchiveIsStrictAndContiguous(t *testing.T) {
	device := snapshotTestID(t, 2)
	operation := coresync.WireOperation{OpID: snapshotTestID(t, 3), WorkspaceID: snapshotTestID(t, 1), DeviceID: device, Sequence: 1,
		Clock: model.HLC{PhysicalMS: 1000, Logical: 1, DeviceID: device}, KeyID: bytes.Repeat([]byte{4}, 16),
		Nonce: bytes.Repeat([]byte{5}, 24), Ciphertext: bytes.Repeat([]byte{6}, 32), Signature: bytes.Repeat([]byte{7}, 64)}
	encoded, err := encodeSnapshotOperations([]coresync.WireOperation{operation})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSnapshotOperations(encoded)
	if err != nil || len(decoded) != 1 || decoded[0].OpID != operation.OpID {
		t.Fatalf("decoded=%v err=%v", decoded, err)
	}
	if _, err := decodeSnapshotOperations(append(encoded, 0)); err == nil {
		t.Fatal("snapshot archive accepted trailing data")
	}
}

func snapshotTestID(t *testing.T, value byte) model.ID {
	t.Helper()
	raw := bytes.Repeat([]byte{value}, 16)
	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80
	id, err := model.ParseID(raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

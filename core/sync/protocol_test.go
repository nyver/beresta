package sync

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

func TestOperationCodecIsDeterministicAndStrict(t *testing.T) {
	op := fixtureWireOperation(t, 0)
	first, err := EncodeOperation(op)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeOperation(op)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("operation encoding is not deterministic")
	}
	decoded, err := DecodeOperation(first, CodecLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if decoded.OpID != op.OpID || !bytes.Equal(decoded.Ciphertext, op.Ciphertext) {
		t.Fatal("operation round trip changed fields")
	}

	// Map length 10 encoded using an unnecessary uint8 is semantically the
	// same value but must be rejected as non-minimal signed material.
	nonMinimal := append([]byte{0xb8, 0x0a}, first[1:]...)
	if _, err := DecodeOperation(nonMinimal, CodecLimits{}); !errors.Is(err, ErrMalformedOperation) {
		t.Fatalf("non-minimal envelope error = %v", err)
	}
	if _, err := DecodeOperation(append(first, 0), CodecLimits{}); !errors.Is(err, ErrMalformedOperation) {
		t.Fatalf("trailing bytes error = %v", err)
	}
}

func TestVersionNegotiation(t *testing.T) {
	if got, err := NegotiateVersion([]uint32{9, 1}); err != nil || got != 1 {
		t.Fatalf("negotiated %d, %v", got, err)
	}
	if _, err := NegotiateVersion([]uint32{2}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("error = %v", err)
	}
}

func FuzzDecodeOperationStrict(f *testing.F) {
	op := fixtureWireOperationForFuzz()
	encoded, _ := EncodeOperation(op)
	f.Add(encoded)
	f.Add([]byte{0xbf, 0xff})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxOperationEnvelopeBytes+1 {
			t.Skip()
		}
		decoded, err := DecodeOperation(input, CodecLimits{})
		if err != nil {
			return
		}
		reencoded, err := EncodeOperation(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(input, reencoded) {
			t.Fatal("decoder accepted a non-canonical envelope")
		}
	})
}

type workerTransport struct {
	calls  []string
	pages  []PullPage
	pushed [][]WireOperation
}

func (t *workerTransport) Pull(_ context.Context, _ model.ID, _ Cursor, _ int) (PullPage, error) {
	t.calls = append(t.calls, "pull")
	page := t.pages[0]
	t.pages = t.pages[1:]
	return page, nil
}
func (t *workerTransport) Push(_ context.Context, _ model.ID, ops []WireOperation) ([]PushResult, error) {
	t.calls = append(t.calls, "push")
	t.pushed = append(t.pushed, ops)
	result := make([]PushResult, len(ops))
	for i, op := range ops {
		result[i] = PushResult{OpID: op.OpID, Sequence: uint64(10 + i)}
	}
	return result, nil
}

type workerRepository struct {
	calls   []string
	cursor  Cursor
	pending []WireOperation
}

func (r *workerRepository) Cursor(context.Context, model.ID) (Cursor, error) { return r.cursor, nil }
func (*workerRepository) Quarantine(context.Context, WireOperation, string, time.Time) error {
	return nil
}
func (*workerRepository) QuarantineBlocked(context.Context, model.ID) (bool, error) {
	return false, nil
}
func (r *workerRepository) ApplyPage(_ context.Context, cursor Cursor, _ []WireOperation, _ OperationProcessor, _ time.Time) error {
	r.calls = append(r.calls, "apply")
	r.cursor = cursor
	return nil
}
func (r *workerRepository) Pending(context.Context, model.ID, int) ([]WireOperation, error) {
	values := r.pending
	r.pending = nil
	return values, nil
}
func (r *workerRepository) MarkPushed(context.Context, model.ID, []PushResult, time.Time) error {
	r.calls = append(r.calls, "mark")
	return nil
}

type acceptingProcessor struct{}

func (acceptingProcessor) Verify(context.Context, WireOperation) (VerifiedOperation, error) {
	return acceptedOperation{}, nil
}

type acceptedOperation struct{}

func (acceptedOperation) Apply(context.Context, SyncTx) error { return nil }

func TestWorkerPullsAppliesThenPushes(t *testing.T) {
	workspace := testID(1)
	remote := fixtureWireOperation(t, 1)
	remote.WorkspaceID = workspace
	remote.Sequence = 1
	local := fixtureWireOperation(t, 2)
	local.WorkspaceID = workspace
	repository := &workerRepository{cursor: Cursor{WorkspaceID: workspace, Epoch: 1}, pending: []WireOperation{local}}
	transport := &workerTransport{pages: []PullPage{{Cursor: Cursor{WorkspaceID: workspace, LastSequence: 1, Epoch: 1}, Operations: []WireOperation{remote}}}}
	worker, err := NewWorker(workspace, repository, transport, acceptingProcessor{}, WorkerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := append(transport.calls, repository.calls...); len(got) != 4 {
		t.Fatalf("calls = %v", got)
	}
	if transport.calls[0] != "pull" || transport.calls[1] != "push" || repository.calls[0] != "apply" || repository.calls[1] != "mark" {
		t.Fatalf("unexpected state transitions: transport=%v repository=%v", transport.calls, repository.calls)
	}
}

type bootstrapTransport struct {
	workspace model.ID
	pulls     int
}

func (t *bootstrapTransport) Pull(_ context.Context, _ model.ID, cursor Cursor, _ int) (PullPage, error) {
	t.pulls++
	if t.pulls == 1 {
		return PullPage{}, ErrSnapshotRequired
	}
	return PullPage{Cursor: Cursor{WorkspaceID: t.workspace, LastSequence: cursor.LastSequence, Epoch: 1}}, nil
}

func (*bootstrapTransport) Push(context.Context, model.ID, []WireOperation) ([]PushResult, error) {
	return nil, nil
}

func TestWorkerBootstrapsExactlyOnceAfterCompaction(t *testing.T) {
	workspace := testID(9)
	repository := &workerRepository{cursor: Cursor{WorkspaceID: workspace, Epoch: 1}}
	transport := &bootstrapTransport{workspace: workspace}
	bootstraps := 0
	worker, err := NewWorker(workspace, repository, transport, acceptingProcessor{}, WorkerOptions{Bootstrap: func(context.Context) error {
		bootstraps++
		repository.cursor.LastSequence = 40
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if bootstraps != 1 || transport.pulls != 2 {
		t.Fatalf("bootstraps=%d pulls=%d", bootstraps, transport.pulls)
	}
}

func BenchmarkWorkspaceSync1000Operations(b *testing.B) {
	workspace := testID(20)
	operations := make([]WireOperation, 1000)
	for index := range operations {
		operations[index] = fixtureWireOperationForFuzz()
		operations[index].OpID = testID(byte(index%200 + 30))
		operations[index].WorkspaceID = workspace
		operations[index].Sequence = uint64(index + 1)
	}
	b.ResetTimer()
	for range b.N {
		cursor := Cursor{WorkspaceID: workspace, Epoch: 1}
		for start := 0; start < len(operations); start += 100 {
			end := min(start+100, len(operations))
			page := PullPage{Cursor: Cursor{WorkspaceID: workspace, LastSequence: uint64(end), Epoch: 1}, Operations: operations[start:end], More: end != len(operations)}
			if err := validatePullPage(workspace, cursor, page, 100); err != nil {
				b.Fatal(err)
			}
			cursor = page.Cursor
		}
	}
}

func TestRandomizedReplicaDeliveryIsExactlyOnce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ids := make([]model.ID, 100)
	for i := range ids {
		ids[i] = testID(byte(i + 40))
	}
	for iteration := 0; iteration < 100; iteration++ {
		permuted := append([]model.ID(nil), ids...)
		rng.Shuffle(len(permuted), func(i, j int) { permuted[i], permuted[j] = permuted[j], permuted[i] })
		applied := map[model.ID]bool{}
		for _, id := range append(permuted, permuted...) {
			applied[id] = true
		}
		if len(applied) != len(ids) {
			t.Fatalf("iteration %d applied %d operations", iteration, len(applied))
		}
	}
}

func fixtureWireOperation(t *testing.T, salt byte) WireOperation {
	t.Helper()
	op := fixtureWireOperationForFuzz()
	op.OpID = testID(3 + salt)
	return op
}
func fixtureWireOperationForFuzz() WireOperation {
	device := testID(2)
	return WireOperation{OpID: testID(3), WorkspaceID: testID(1), DeviceID: device, Clock: model.HLC{PhysicalMS: 1000, Logical: 2, DeviceID: device}, KeyID: bytes.Repeat([]byte{4}, 16), Nonce: bytes.Repeat([]byte{5}, 24), Ciphertext: bytes.Repeat([]byte{6}, 32), Signature: bytes.Repeat([]byte{7}, 64)}
}
func testID(salt byte) model.ID {
	raw := bytes.Repeat([]byte{salt}, 16)
	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80
	id, _ := model.ParseID(raw)
	return id
}

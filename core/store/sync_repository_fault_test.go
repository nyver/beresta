package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
)

// syncTestWireOperation builds a WireOperation with correctly sized
// cryptographic fields (unlike testWireOperation's shorter placeholders,
// which are fine for the Quarantine-only tests above but fail
// EncodeOperation's field-length validation that ApplyPage relies on for
// its envelope digest).
func syncTestWireOperation(t testing.TB, workspaceID model.ID, sequence uint64, deviceSeed byte, cipherSeed byte) coresync.WireOperation {
	t.Helper()
	opID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	device := repoTestDeviceID(t, deviceSeed)
	return coresync.WireOperation{
		OpID: opID, WorkspaceID: workspaceID, DeviceID: device,
		Clock:      repoClock(t, 1000+sequence, 0, deviceSeed),
		KeyID:      make([]byte, 16),
		Nonce:      make([]byte, 24),
		Ciphertext: bytes.Repeat([]byte{cipherSeed}, 32),
		Signature:  make([]byte, 64),
		Sequence:   sequence,
	}
}

// fakeVerifiedOperation runs applyFn (or succeeds trivially when nil)
// against the transaction ApplyPage supplies, and counts how many times
// Apply actually ran so tests can prove a duplicate delivery is not
// re-applied.
type fakeVerifiedOperation struct {
	applyFn func(context.Context, coresync.SyncTx) error
	applied *int
}

func (v fakeVerifiedOperation) Apply(ctx context.Context, tx coresync.SyncTx) error {
	if v.applied != nil {
		*v.applied++
	}
	if v.applyFn == nil {
		return nil
	}
	return v.applyFn(ctx, tx)
}

// fakeProcessor verifies every operation successfully by default; perOp
// customizes one operation's VerifiedOperation by OpID (for example to
// fail its Apply, or to count how many times it actually ran).
type fakeProcessor struct {
	perOp map[model.ID]fakeVerifiedOperation
}

func (p fakeProcessor) Verify(_ context.Context, op coresync.WireOperation) (coresync.VerifiedOperation, error) {
	if v, ok := p.perOp[op.OpID]; ok {
		return v, nil
	}
	return fakeVerifiedOperation{}, nil
}

// TestSyncRepositoryApplyPageRollsBackTheEntireBatchWhenAMidBatchOperationFails
// covers task 3.8's "process termination during ... apply" regression: a
// forced process kill mid-ApplyPage and a rejected operation mid-batch are
// indistinguishable from the database's point of view, since neither case
// reaches tx.Commit(). A rejection on the second of two operations must
// roll back the first operation's effects too - not leave it half-applied -
// so a retried page starting from the same prior cursor can apply cleanly.
func TestSyncRepositoryApplyPageRollsBackTheEntireBatchWhenAMidBatchOperationFails(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	repo, err := NewSyncRepository(db, "test-transport")
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := seedWorkspace(t, db)

	first := syncTestWireOperation(t, workspaceID, 1, 0x10, 0xa1)
	second := syncTestWireOperation(t, workspaceID, 2, 0x10, 0xa2)
	var firstApplied int
	processor := fakeProcessor{perOp: map[model.ID]fakeVerifiedOperation{
		first.OpID:  {applied: &firstApplied},
		second.OpID: {applyFn: func(context.Context, coresync.SyncTx) error { return errors.New("simulated apply failure") }},
	}}

	cursor := coresync.Cursor{WorkspaceID: workspaceID, Epoch: 1, LastSequence: 2}
	err = repo.ApplyPage(ctx, cursor, []coresync.WireOperation{first, second}, processor, time.Now())
	var rejected *coresync.RejectedOperationError
	if !errors.As(err, &rejected) || rejected.Class != "apply_failed" {
		t.Fatalf("ApplyPage error = %v, want a RejectedOperationError with class apply_failed", err)
	}
	if firstApplied != 1 {
		t.Fatalf("first operation's Apply ran %d times before the batch failed", firstApplied)
	}

	got, err := repo.Cursor(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSequence != 0 {
		t.Fatalf("cursor after rollback = %d, want 0 (unchanged - nothing in the failed batch may advance it)", got.LastSequence)
	}

	entries, err := repo.ListQuarantine(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("quarantine entries after a mid-batch Apply failure = %+v, want none (ApplyPage itself only rolls back; quarantining is the caller's job)", entries)
	}

	// A retry starting from the same prior cursor, with the first
	// operation's Apply now succeeding, must find no trace of the rolled
	// back attempt: firstApplied resets to 0 confirms this is a clean
	// re-application, not a second increment on top of a partial commit.
	firstApplied = 0
	processor = fakeProcessor{perOp: map[model.ID]fakeVerifiedOperation{first.OpID: {applied: &firstApplied}}}
	if err := repo.ApplyPage(ctx, coresync.Cursor{WorkspaceID: workspaceID, Epoch: 1, LastSequence: 1}, []coresync.WireOperation{first}, processor, time.Now()); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if firstApplied != 1 {
		t.Fatalf("first operation's Apply ran %d times on retry, want exactly 1", firstApplied)
	}
	got, err = repo.Cursor(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSequence != 1 {
		t.Fatalf("cursor after retry = %d, want 1", got.LastSequence)
	}
}

// TestSyncRepositoryApplyPageRejectsOperationIDReuseWithDifferentContent
// covers task 3.8's "corrupt ... operations" regression: a page that
// reuses an already-applied operation identifier for genuinely different
// content (whether from a buggy or actively malicious server) must be
// rejected rather than silently overwriting what the identifier already
// means locally.
func TestSyncRepositoryApplyPageRejectsOperationIDReuseWithDifferentContent(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	repo, err := NewSyncRepository(db, "test-transport")
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := seedWorkspace(t, db)

	original := syncTestWireOperation(t, workspaceID, 1, 0x11, 0xb1)
	reused := original
	reused.Sequence = 2
	reused.Ciphertext = append([]byte(nil), original.Ciphertext...)
	reused.Ciphertext[0] ^= 0xFF // same OpID, genuinely different content

	processor := fakeProcessor{}
	cursor := coresync.Cursor{WorkspaceID: workspaceID, Epoch: 1, LastSequence: 2}
	err = repo.ApplyPage(ctx, cursor, []coresync.WireOperation{original, reused}, processor, time.Now())
	var rejected *coresync.RejectedOperationError
	if !errors.As(err, &rejected) || rejected.Class != "op_id_reuse" {
		t.Fatalf("ApplyPage error = %v, want a RejectedOperationError with class op_id_reuse", err)
	}

	got, err := repo.Cursor(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSequence != 0 {
		t.Fatalf("cursor after rejected reuse = %d, want 0 (the whole batch, including the first, valid operation, rolls back)", got.LastSequence)
	}
}

// TestSyncRepositoryApplyPageIsIdempotentForAnExactDuplicateWithinAPage
// covers task 3.8's "duplicate ... operations" regression: the exact same
// operation appearing twice (a resent page overlapping one already seen,
// or a retried delivery) must be applied at most once, never twice.
func TestSyncRepositoryApplyPageIsIdempotentForAnExactDuplicateWithinAPage(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	repo, err := NewSyncRepository(db, "test-transport")
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := seedWorkspace(t, db)

	op := syncTestWireOperation(t, workspaceID, 1, 0x12, 0xc1)
	var applied int
	processor := fakeProcessor{perOp: map[model.ID]fakeVerifiedOperation{op.OpID: {applied: &applied}}}

	cursor := coresync.Cursor{WorkspaceID: workspaceID, Epoch: 1, LastSequence: 1}
	if err := repo.ApplyPage(ctx, cursor, []coresync.WireOperation{op, op}, processor, time.Now()); err != nil {
		t.Fatalf("ApplyPage with a duplicated operation: %v", err)
	}
	if applied != 1 {
		t.Fatalf("Apply ran %d times for a page containing the same operation twice, want exactly 1", applied)
	}

	got, err := repo.Cursor(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSequence != 1 {
		t.Fatalf("cursor = %d, want 1", got.LastSequence)
	}
}

// TestSyncRepositoryMarkPushedRollsBackTheWholeCallWhenAResultIsInvalid
// covers task 3.8's "process termination during ... push" regression, the
// push-side analog of the apply rollback test above: MarkPushed processes
// every result in one transaction (see sync_repository.go), so an invalid
// result partway through a call must not leave an earlier, valid result's
// update committed.
func TestSyncRepositoryMarkPushedRollsBackTheWholeCallWhenAResultIsInvalid(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	repo, err := NewSyncRepository(db, "test-transport")
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := seedWorkspace(t, db)

	valid := OutboxOperation{
		OpID: mustNewID(t), WorkspaceID: workspaceID, DeviceID: repoTestDeviceID(t, 0x13),
		Clock: repoClock(t, 1000, 0, 0x13),
		KeyID: []byte("key"), Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext"), Signature: []byte("sig"),
	}
	if err := InsertOutboxOperation(ctx, db, valid, 1000); err != nil {
		t.Fatal(err)
	}

	// The second result targets an operation identifier that was never
	// pushed at all, so its UPDATE affects zero rows - exactly the
	// defense-in-depth check MarkPushed uses to catch a malformed response.
	results := []coresync.PushResult{
		{OpID: valid.OpID, Sequence: 5},
		{OpID: mustNewID(t), Sequence: 6},
	}
	if err := repo.MarkPushed(ctx, workspaceID, results, time.Now()); err == nil {
		t.Fatal("expected MarkPushed to reject a result batch containing an unmatched operation")
	}

	count, err := repo.CountPending(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("CountPending after the rejected batch = %d, want 1 (the valid result's update must have rolled back too)", count)
	}

	pending, err := repo.Pending(ctx, workspaceID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].OpID != valid.OpID {
		t.Fatalf("Pending after the rejected batch = %+v, want the original operation still outstanding", pending)
	}
}

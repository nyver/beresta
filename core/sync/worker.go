package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

var (
	ErrWorkspaceQuarantined = errors.New("sync: workspace is blocked by a quarantined operation")
	ErrInvalidCursor        = errors.New("sync: invalid cursor transition")
	ErrSnapshotRequired     = errors.New("sync: encrypted snapshot bootstrap required")
)

type Cursor struct {
	WorkspaceID  model.ID
	LastSequence uint64
	Epoch        uint32
}

type PullPage struct {
	Cursor     Cursor
	Operations []WireOperation
	More       bool
}

type PushResult struct {
	OpID           model.ID
	Sequence       uint64
	Duplicate      bool
	PermanentError string
}

// OperationTransport exchanges opaque operations. Blob, snapshot, and hint
// capabilities are separate optional interfaces so local-only operation does
// not need fake network behavior.
type OperationTransport interface {
	Pull(context.Context, model.ID, Cursor, int) (PullPage, error)
	Push(context.Context, model.ID, []WireOperation) ([]PushResult, error)
}

type CursorSubscriber interface {
	Subscribe(context.Context, model.ID) (<-chan Cursor, error)
}

type SyncTx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// VerifiedOperation contains a fully authenticated and decrypted operation.
// Apply must only mutate through the transaction supplied by the repository.
type VerifiedOperation interface {
	Apply(context.Context, SyncTx) error
}

type OperationProcessor interface {
	Verify(context.Context, WireOperation) (VerifiedOperation, error)
}

type WorkspaceRepository interface {
	Cursor(context.Context, model.ID) (Cursor, error)
	Quarantine(context.Context, WireOperation, string, time.Time) error
	QuarantineBlocked(context.Context, model.ID) (bool, error)
	ApplyPage(context.Context, Cursor, []WireOperation, OperationProcessor, time.Time) error
	Pending(context.Context, model.ID, int) ([]WireOperation, error)
	MarkPushed(context.Context, model.ID, []PushResult, time.Time) error
}

type Phase string

const (
	PhasePull       Phase = "pull"
	PhaseApply      Phase = "apply"
	PhasePush       Phase = "push"
	PhaseCurrent    Phase = "current"
	PhaseBackoff    Phase = "backoff"
	PhaseQuarantine Phase = "quarantine"
)

type Progress struct {
	WorkspaceID model.ID
	Phase       Phase
	Pulled      int
	Pushed      int
	Cursor      uint64
	RetryIn     time.Duration
	ErrorClass  string
	// ErrorDetail is a bounded, diagnostic-only description. It never
	// contains operation ciphertext or key material.
	ErrorDetail string
}

type WorkerOptions struct {
	BatchSize       int
	PollInterval    time.Duration
	InitialBackoff  time.Duration
	MaxBackoff      time.Duration
	Now             func() time.Time
	Jitter          func(time.Duration) time.Duration
	Progress        func(Progress)
	Prepare         func(context.Context) error
	Bootstrap       func(context.Context) error
	ReviewSnapshot  func(context.Context, Cursor) error
	SyncAttachments func(context.Context) error
	PublishSnapshot func(context.Context, Cursor) error
}

func (o WorkerOptions) normalized() WorkerOptions {
	if o.BatchSize <= 0 || o.BatchSize > 256 {
		o.BatchSize = 100
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 30 * time.Second
	}
	if o.InitialBackoff <= 0 {
		o.InitialBackoff = time.Second
	}
	if o.MaxBackoff < o.InitialBackoff {
		o.MaxBackoff = time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Jitter == nil {
		o.Jitter = func(cap time.Duration) time.Duration {
			if cap <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(cap) + 1))
		}
	}
	return o
}

type Worker struct {
	workspace  model.ID
	repository WorkspaceRepository
	transport  OperationTransport
	processor  OperationProcessor
	options    WorkerOptions
}

func NewWorker(workspace model.ID, repository WorkspaceRepository, transport OperationTransport, processor OperationProcessor, options WorkerOptions) (*Worker, error) {
	if err := workspace.Validate(); err != nil || repository == nil || transport == nil || processor == nil {
		return nil, errors.New("sync: invalid worker dependency")
	}
	return &Worker{workspace: workspace, repository: repository, transport: transport, processor: processor, options: options.normalized()}, nil
}

// SyncOnce runs one complete pull-verify-apply-then-push cycle.
func (w *Worker) SyncOnce(ctx context.Context) error {
	if w.options.Prepare != nil {
		if err := w.options.Prepare(ctx); err != nil {
			return fmt.Errorf("sync: prepare: %w", err)
		}
	}
	blocked, err := w.repository.QuarantineBlocked(ctx, w.workspace)
	if err != nil {
		return err
	}
	if blocked {
		w.emit(Progress{WorkspaceID: w.workspace, Phase: PhaseQuarantine, ErrorClass: "quarantined_operation", ErrorDetail: "a quarantined operation is blocking synchronization"})
		return ErrWorkspaceQuarantined
	}

	cursor, err := w.repository.Cursor(ctx, w.workspace)
	if err != nil {
		return fmt.Errorf("sync: load cursor: %w", err)
	}
	bootstrapped := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		w.emit(Progress{WorkspaceID: w.workspace, Phase: PhasePull, Cursor: cursor.LastSequence})
		page, err := w.transport.Pull(ctx, w.workspace, cursor, w.options.BatchSize)
		if err != nil {
			if errors.Is(err, ErrSnapshotRequired) && !bootstrapped && w.options.Bootstrap != nil {
				if err := w.options.Bootstrap(ctx); err != nil {
					return fmt.Errorf("sync: snapshot bootstrap: %w", err)
				}
				cursor, err = w.repository.Cursor(ctx, w.workspace)
				if err != nil {
					return fmt.Errorf("sync: load bootstrapped cursor: %w", err)
				}
				bootstrapped = true
				continue
			}
			return fmt.Errorf("sync: pull: %w", err)
		}
		if err := validatePullPage(w.workspace, cursor, page, w.options.BatchSize); err != nil {
			return err
		}
		if len(page.Operations) != 0 {
			w.emit(Progress{WorkspaceID: w.workspace, Phase: PhaseApply, Pulled: len(page.Operations), Cursor: cursor.LastSequence})
			if err := w.repository.ApplyPage(ctx, page.Cursor, page.Operations, w.processor, w.options.Now()); err != nil {
				var bad *RejectedOperationError
				if errors.As(err, &bad) {
					if quarantineErr := w.repository.Quarantine(ctx, bad.Operation, bad.Class, w.options.Now()); quarantineErr != nil {
						return errors.Join(err, quarantineErr)
					}
					w.emit(Progress{WorkspaceID: w.workspace, Phase: PhaseQuarantine, Cursor: cursor.LastSequence, ErrorClass: bad.Class, ErrorDetail: syncErrorDetail(bad.Err)})
					return ErrWorkspaceQuarantined
				}
				return fmt.Errorf("sync: apply page: %w", err)
			}
			cursor = page.Cursor
		}
		if !page.More {
			break
		}
	}
	if w.options.ReviewSnapshot != nil {
		if err := w.options.ReviewSnapshot(ctx, cursor); err != nil {
			return fmt.Errorf("sync: review snapshot: %w", err)
		}
	}
	if w.options.SyncAttachments != nil {
		if err := w.options.SyncAttachments(ctx); err != nil {
			return fmt.Errorf("sync: attachments: %w", err)
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		pending, err := w.repository.Pending(ctx, w.workspace, w.options.BatchSize)
		if err != nil {
			return fmt.Errorf("sync: read outbox: %w", err)
		}
		if len(pending) == 0 {
			break
		}
		w.emit(Progress{WorkspaceID: w.workspace, Phase: PhasePush, Pushed: len(pending), Cursor: cursor.LastSequence})
		results, err := w.transport.Push(ctx, w.workspace, pending)
		if err != nil {
			return fmt.Errorf("sync: push: %w", err)
		}
		if err := validatePushResults(pending, results); err != nil {
			return err
		}
		if err := w.repository.MarkPushed(ctx, w.workspace, results, w.options.Now()); err != nil {
			return fmt.Errorf("sync: commit push results: %w", err)
		}
		if len(pending) < w.options.BatchSize {
			break
		}
	}
	if cursor.LastSequence != 0 && w.options.PublishSnapshot != nil {
		if err := w.options.PublishSnapshot(ctx, cursor); err != nil {
			return fmt.Errorf("sync: publish snapshot: %w", err)
		}
	}
	w.emit(Progress{WorkspaceID: w.workspace, Phase: PhaseCurrent, Cursor: cursor.LastSequence})
	return nil
}

// Run coalesces timer, foreground, local-change, and cursor-hint triggers into
// a single workspace worker and applies capped full-jitter retry.
func (w *Worker) Run(ctx context.Context, triggers <-chan struct{}) error {
	backoff := w.options.InitialBackoff
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-triggers:
			stopAndDrain(timer)
		case <-timer.C:
		}

		err := w.SyncOnce(ctx)
		if err == nil {
			backoff = w.options.InitialBackoff
			timer.Reset(w.options.PollInterval)
			continue
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, ErrWorkspaceQuarantined) {
			return err
		}
		delay := w.options.Jitter(backoff)
		w.emit(Progress{WorkspaceID: w.workspace, Phase: PhaseBackoff, RetryIn: delay, ErrorClass: classifySyncError(err), ErrorDetail: syncErrorDetail(err)})
		timer.Reset(delay)
		if backoff < w.options.MaxBackoff/2 {
			backoff *= 2
		} else {
			backoff = w.options.MaxBackoff
		}
	}
}

type RejectedOperationError struct {
	Operation WireOperation
	Class     string
	Err       error
}

func (e *RejectedOperationError) Error() string { return "sync: rejected operation: " + e.Class }
func (e *RejectedOperationError) Unwrap() error { return e.Err }

func Reject(op WireOperation, class string, err error) error {
	if class == "" {
		class = "invalid_operation"
	}
	return &RejectedOperationError{Operation: op, Class: class, Err: err}
}

func validatePullPage(workspace model.ID, previous Cursor, page PullPage, limit int) error {
	if page.Cursor.WorkspaceID != workspace || page.Cursor.Epoch == 0 || len(page.Operations) > limit {
		return ErrInvalidCursor
	}
	if previous.Epoch != 0 && page.Cursor.Epoch != previous.Epoch {
		return ErrInvalidCursor
	}
	expected := previous.LastSequence + 1
	for _, operation := range page.Operations {
		if operation.WorkspaceID != workspace || operation.Sequence != expected {
			return ErrInvalidCursor
		}
		expected++
	}
	if len(page.Operations) != 0 && page.Cursor.LastSequence != expected-1 {
		return ErrInvalidCursor
	}
	if len(page.Operations) == 0 && page.Cursor.LastSequence < previous.LastSequence {
		return ErrInvalidCursor
	}
	return nil
}

func validatePushResults(pending []WireOperation, results []PushResult) error {
	if len(results) != len(pending) {
		return errors.New("sync: push response does not cover the submitted batch")
	}
	seen := make(map[model.ID]bool, len(results))
	for _, result := range results {
		if (result.Sequence == 0) == (result.PermanentError == "") || seen[result.OpID] {
			return errors.New("sync: invalid push response")
		}
		seen[result.OpID] = true
	}
	for _, operation := range pending {
		if !seen[operation.OpID] {
			return errors.New("sync: push response omitted an operation")
		}
	}
	return nil
}

func (w *Worker) emit(progress Progress) {
	if w.options.Progress != nil {
		w.options.Progress(progress)
	}
}

func stopAndDrain(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func classifySyncError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrUnsupportedVersion):
		return "unsupported_version"
	default:
		return "transient_transport"
	}
}

func syncErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	const maxDetailBytes = 512
	message := err.Error()
	if len(message) <= maxDetailBytes {
		return message
	}
	return message[:maxDetailBytes-3] + "..."
}

package sync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

// TestWorkerBackoffDoublesThenCapsAtMaxBackoff proves the exponential
// backoff arithmetic in Worker.Run: the delay between consecutive failed
// cycles doubles each time, until it would exceed MaxBackoff/2, after which
// it is pinned exactly at MaxBackoff instead of continuing to grow.
func TestWorkerBackoffDoublesThenCapsAtMaxBackoff(t *testing.T) {
	workspaceID := testID(50)
	repository := &workerRepository{cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}}
	progressCh := make(chan Progress, 32)
	worker, err := NewWorker(workspaceID, repository, failingWorkerTransport{}, acceptingProcessor{}, WorkerOptions{
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     80 * time.Millisecond,
		// Identity jitter isolates the doubling/capping arithmetic under
		// test from jitter's own randomization (covered separately below).
		Jitter:   func(cap time.Duration) time.Duration { return cap },
		Progress: func(p Progress) { progressCh <- p },
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, make(chan struct{})) }()
	defer func() {
		cancel()
		<-done
	}()

	want := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		80 * time.Millisecond,
		80 * time.Millisecond,
		80 * time.Millisecond,
	}
	var got []time.Duration
	deadline := time.After(2 * time.Second)
	for len(got) < len(want) {
		select {
		case p := <-progressCh:
			if p.Phase == PhaseBackoff {
				got = append(got, p.RetryIn)
			}
		case <-deadline:
			t.Fatalf("only observed %d backoff events before timing out, want %d: %v", len(got), len(want), got)
		}
	}

	for i, w := range want {
		if got[i] != w {
			t.Fatalf("backoff[%d] = %v, want %v (full sequence: %v)", i, got[i], w, got)
		}
	}
}

// TestWorkerBackoffRetryInComesFromTheInjectedJitterFunction proves RetryIn
// reflects whatever the injected Jitter function returns, not the raw
// doubling backoff value - a fixed jitter output distinct from every raw
// backoff value in the sequence can only appear in Progress.RetryIn if
// Jitter's return value is actually what gets used.
func TestWorkerBackoffRetryInComesFromTheInjectedJitterFunction(t *testing.T) {
	workspaceID := testID(51)
	repository := &workerRepository{cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}}
	progressCh := make(chan Progress, 32)
	const fixedJitter = 7 * time.Millisecond
	worker, err := NewWorker(workspaceID, repository, failingWorkerTransport{}, acceptingProcessor{}, WorkerOptions{
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     time.Second,
		Jitter:         func(time.Duration) time.Duration { return fixedJitter },
		Progress:       func(p Progress) { progressCh <- p },
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, make(chan struct{})) }()
	defer func() {
		cancel()
		<-done
	}()

	observed := 0
	deadline := time.After(2 * time.Second)
	for observed < 3 {
		select {
		case p := <-progressCh:
			if p.Phase != PhaseBackoff {
				continue
			}
			if p.RetryIn != fixedJitter {
				t.Fatalf("RetryIn = %v, want the injected jitter's fixed output %v", p.RetryIn, fixedJitter)
			}
			observed++
		case <-deadline:
			t.Fatalf("only observed %d backoff events before timing out, want 3", observed)
		}
	}
}

// fakeTrustFailure implements trustFailure so tests can prove
// classifySyncError recognizes a transport's trust-boundary error
// structurally, exactly as core/transport's real ErrCertificatePin and
// ErrAuthentication do, without this package importing core/transport.
type fakeTrustFailure struct{}

func (fakeTrustFailure) Error() string      { return "sync: fake trust failure" }
func (fakeTrustFailure) TrustFailure() bool { return true }

// TestClassifySyncErrorRecognizesTrustFailureStructurally covers task 3.8's
// "TLS identity change" regression: classifySyncError must find a
// trustFailure even after the transport/worker layers wrap it with %w (the
// same wrapping Worker.SyncOnce applies to every transport error), and must
// not misclassify an ordinary error as one.
func TestClassifySyncErrorRecognizesTrustFailureStructurally(t *testing.T) {
	wrapped := fmt.Errorf("sync: pull: %w", fakeTrustFailure{})
	if got := classifySyncError(wrapped); got != "trust_or_configuration" {
		t.Fatalf("classifySyncError(wrapped trust failure) = %q, want trust_or_configuration", got)
	}
	if got := classifySyncError(errors.New("sync: pull: connection refused")); got != "transient_transport" {
		t.Fatalf("classifySyncError(ordinary error) = %q, want transient_transport", got)
	}
}

// trustFailingTransport always fails Pull with a trust-boundary error,
// mirroring what core/transport.HTTP returns when a pinned fingerprint no
// longer matches the server's certificate.
type trustFailingTransport struct{}

func (trustFailingTransport) Pull(context.Context, model.ID, Cursor, int) (PullPage, error) {
	return PullPage{}, fmt.Errorf("sync: pull: %w", fakeTrustFailure{})
}
func (trustFailingTransport) Push(context.Context, model.ID, []WireOperation) ([]PushResult, error) {
	return nil, nil
}

// TestWorkerReportsTrustFailureAsActionableNotOffline covers task 3.8's
// "TLS identity change" regression end to end through Worker.Run: a
// certificate/authentication trust-boundary failure must reach Progress as
// the "trust_or_configuration" class - which core/syncsummary.Summarize
// routes to action_required/review_connection - rather than the generic
// "transient_transport" class a routine connectivity hiccup produces,
// which would instead surface as an ordinary, dismissible offline state
// (specs/release-quality's "TLS identity changes" scenario: "trust action
// is requested").
func TestWorkerReportsTrustFailureAsActionableNotOffline(t *testing.T) {
	workspaceID := testID(53)
	repository := &workerRepository{cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}}
	progressCh := make(chan Progress, 32)
	worker, err := NewWorker(workspaceID, repository, trustFailingTransport{}, acceptingProcessor{}, WorkerOptions{
		InitialBackoff: 10 * time.Second,
		MaxBackoff:     time.Minute,
		Progress:       func(p Progress) { progressCh <- p },
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, make(chan struct{})) }()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-progressCh:
			if p.Phase != PhaseBackoff {
				continue
			}
			if p.ErrorClass != "trust_or_configuration" {
				t.Fatalf("ErrorClass = %q, want trust_or_configuration", p.ErrorClass)
			}
			return
		case <-deadline:
			t.Fatal("never observed a backoff progress event")
		}
	}
}

// onceFailingTransport fails its first Pull call and succeeds on every
// call after, recording how many calls overlapped in flight (maxSeen) and
// how many total calls happened.
type onceFailingTransport struct {
	mu      sync.Mutex
	calls   int
	active  int
	maxSeen int
}

func (t *onceFailingTransport) Pull(_ context.Context, workspaceID model.ID, cursor Cursor, _ int) (PullPage, error) {
	t.mu.Lock()
	t.calls++
	call := t.calls
	t.active++
	if t.active > t.maxSeen {
		t.maxSeen = t.active
	}
	t.mu.Unlock()

	time.Sleep(2 * time.Millisecond)

	t.mu.Lock()
	t.active--
	t.mu.Unlock()

	if call == 1 {
		return PullPage{}, errors.New("simulated transient failure")
	}
	return PullPage{Cursor: Cursor{WorkspaceID: workspaceID, Epoch: cursor.Epoch, LastSequence: cursor.LastSequence}}, nil
}

func (*onceFailingTransport) Push(context.Context, model.ID, []WireOperation) ([]PushResult, error) {
	return nil, nil
}

// TestWorkerTriggerDuringBackoffAdvancesRetryWithoutOverlap is the
// "attempt-now wake-up" case task 3.3 is about: a foreground or manual
// retry request arriving while the worker is waiting out a backoff delay
// must advance the retry immediately instead of waiting for the full delay
// to elapse, and must do so through the same coalescing trigger channel
// Run's select loop already listens on - never by starting a second,
// overlapping cycle.
func TestWorkerTriggerDuringBackoffAdvancesRetryWithoutOverlap(t *testing.T) {
	workspaceID := testID(52)
	repository := &workerRepository{cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}}
	transport := &onceFailingTransport{}
	progressCh := make(chan Progress, 32)
	worker, err := NewWorker(workspaceID, repository, transport, acceptingProcessor{}, WorkerOptions{
		// Deliberately much longer than the test's own timeout: if Trigger
		// did not advance the wait, the test fails instead of merely
		// running slowly.
		InitialBackoff: 10 * time.Second,
		MaxBackoff:     time.Minute,
		Jitter:         func(cap time.Duration) time.Duration { return cap },
		Progress:       func(p Progress) { progressCh <- p },
	})
	if err != nil {
		t.Fatal(err)
	}

	triggers := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, triggers) }()
	defer func() {
		cancel()
		<-done
	}()

	waitForPhase := func(want Phase, timeout time.Duration) {
		t.Helper()
		deadline := time.After(timeout)
		for {
			select {
			case p := <-progressCh:
				if p.Phase == want {
					return
				}
			case <-deadline:
				t.Fatalf("never observed phase %q", want)
			}
		}
	}

	waitForPhase(PhaseBackoff, time.Second)

	select {
	case triggers <- struct{}{}:
	default:
		t.Fatal("could not queue a trigger")
	}

	// If the trigger genuinely woke the backoff wait early, the retried
	// cycle succeeds and reaches PhaseCurrent almost immediately - well
	// before the 10s backoff would ever have elapsed on its own.
	waitForPhase(PhaseCurrent, 2*time.Second)

	transport.mu.Lock()
	maxSeen, calls := transport.maxSeen, transport.calls
	transport.mu.Unlock()
	if maxSeen > 1 {
		t.Fatalf("observed %d overlapping Pull calls, want at most 1 - the triggered retry must not race an already-running cycle", maxSeen)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want exactly 2 (the initial failure, then the triggered retry) - coalescing must not produce extra cycles", calls)
	}
}

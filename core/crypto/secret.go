package crypto

import (
	"errors"
	"runtime"
	"sync"
)

// MaxSecretBytes bounds one owned secret allocation. Large plaintext payloads
// use streaming APIs instead of the live-key buffer.
const MaxSecretBytes = 1 << 20

var (
	// ErrSecretClosed reports use after the secret was wiped.
	ErrSecretClosed = errors.New("crypto: secret is closed")
	// ErrInvalidSecretSize reports an empty or excessively large allocation.
	ErrInvalidSecretSize = errors.New("crypto: invalid secret size")
	// ErrInvalidSecretUse reports a nil access callback.
	ErrInvalidSecretUse = errors.New("crypto: invalid secret use callback")
)

// Secret owns one mutable byte allocation for a bounded key lifetime.
//
// Callers relinquish the complete backing array passed to TakeSecret and must
// not retain slices received by Use. Secret minimizes copies, but Go cannot
// guarantee erasure of runtime, stack, register, or library-internal copies.
type Secret struct {
	mu      sync.Mutex
	storage []byte
	length  int
	closed  bool
}

// TakeSecret takes ownership of value and its complete backing array without
// copying. On validation failure it wipes the relinquished allocation.
func TakeSecret(value []byte) (*Secret, error) {
	storage := value
	if cap(value) > 0 {
		storage = value[:cap(value)]
	}
	if len(value) == 0 || cap(value) > MaxSecretBytes {
		wipe(storage)
		return nil, ErrInvalidSecretSize
	}

	return &Secret{
		storage: storage,
		length:  len(value),
	}, nil
}

// Use exposes the logical secret bytes only for the duration of fn. The slice
// has no spare capacity. A callback error, panic, or goroutine exit wipes and
// closes the secret before control leaves Use.
//
// The callback must not retain the slice or call methods on this Secret.
func (s *Secret) Use(fn func([]byte) error) (err error) {
	if s == nil {
		return ErrSecretClosed
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSecretClosed
	}
	if fn == nil {
		s.destroyLocked()
		return ErrInvalidSecretUse
	}

	completed := false
	defer func() {
		recovered := recover()
		if !completed || err != nil {
			s.destroyLocked()
		}
		if recovered != nil {
			panic(recovered)
		}
	}()

	err = fn(s.storage[:s.length:s.length])
	completed = true
	return err
}

// Len returns the logical byte length, or zero after the secret is closed.
func (s *Secret) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0
	}
	return s.length
}

// Wipe destroys the secret immediately. Account lock and cancellation paths
// use this explicit lifecycle alias for Close.
func (s *Secret) Wipe() {
	s.Close()
}

// Close wipes the complete owned backing array and releases it. It is
// idempotent and waits for an in-flight Use callback to return.
func (s *Secret) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.destroyLocked()
}

// String prevents ordinary formatting and logging from exposing secret bytes.
func (s *Secret) String() string {
	return "[redacted secret]"
}

// GoString prevents %#v formatting from exposing the private backing array.
func (s *Secret) GoString() string {
	return "crypto.Secret{redacted}"
}

func (s *Secret) destroyLocked() {
	if s.closed {
		return
	}
	wipe(s.storage)
	s.storage = nil
	s.length = 0
	s.closed = true
}

func wipe(value []byte) {
	clear(value)
	// Keep the allocation live until after clear so cleanup remains an explicit
	// observable write at the lifecycle boundary.
	runtime.KeepAlive(value)
}

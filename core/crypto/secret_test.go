package crypto

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestSecretUsesOwnedAllocationAndWipesOnClose(t *testing.T) {
	backing := make([]byte, 32, 64)
	copy(backing, bytes.Repeat([]byte{0x5a}, len(backing)))
	fullAllocation := backing[:cap(backing)]
	secret, err := TakeSecret(backing)
	if err != nil {
		t.Fatal(err)
	}
	if secret.Len() != len(backing) {
		t.Fatalf("Len() = %d, want %d", secret.Len(), len(backing))
	}

	err = secret.Use(func(value []byte) error {
		if len(value) != cap(value) {
			t.Fatalf("callback capacity = %d, want %d", cap(value), len(value))
		}
		value[0] = 0xa5
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if backing[0] != 0xa5 {
		t.Fatal("TakeSecret copied the relinquished allocation")
	}

	secret.Close()
	secret.Close()
	if secret.Len() != 0 {
		t.Fatalf("closed Len() = %d, want 0", secret.Len())
	}
	if !allZero(fullAllocation) {
		t.Fatal("Close did not wipe the complete backing allocation")
	}
	if err := secret.Use(func([]byte) error { return nil }); !errors.Is(err, ErrSecretClosed) {
		t.Fatalf("closed Use error = %v", err)
	}
}

func TestSecretWipesOnLockAndCallbackErrors(t *testing.T) {
	t.Run("lock", func(t *testing.T) {
		value := bytes.Repeat([]byte{0x11}, 32)
		secret, err := TakeSecret(value)
		if err != nil {
			t.Fatal(err)
		}
		secret.Wipe()
		if !allZero(value) {
			t.Fatal("Wipe did not clear the secret on the lock path")
		}
	})

	t.Run("returned error", func(t *testing.T) {
		value := bytes.Repeat([]byte{0x22}, 32)
		secret, err := TakeSecret(value)
		if err != nil {
			t.Fatal(err)
		}
		wantErr := errors.New("operation failed")
		if err := secret.Use(func([]byte) error { return wantErr }); !errors.Is(err, wantErr) {
			t.Fatalf("Use error = %v", err)
		}
		if !allZero(value) {
			t.Fatal("callback error did not wipe the secret")
		}
	})

	t.Run("nil callback", func(t *testing.T) {
		value := bytes.Repeat([]byte{0x33}, 32)
		secret, err := TakeSecret(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := secret.Use(nil); !errors.Is(err, ErrInvalidSecretUse) {
			t.Fatalf("Use error = %v", err)
		}
		if !allZero(value) {
			t.Fatal("nil callback did not wipe the secret")
		}
	})
}

func TestSecretWipesBeforeRepanicking(t *testing.T) {
	value := bytes.Repeat([]byte{0x44}, 32)
	secret, err := TakeSecret(value)
	if err != nil {
		t.Fatal(err)
	}

	func() {
		defer func() {
			if recovered := recover(); recovered != "sentinel panic" {
				t.Fatalf("recovered = %v", recovered)
			}
		}()
		_ = secret.Use(func([]byte) error {
			panic("sentinel panic")
		})
	}()

	if !allZero(value) {
		t.Fatal("panic path did not wipe the secret")
	}
}

func TestSecretWipesWhenGoroutineExitsFromCallback(t *testing.T) {
	value := bytes.Repeat([]byte{0x55}, 32)
	secret, err := TakeSecret(value)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = secret.Use(func([]byte) error {
			runtime.Goexit()
			return nil
		})
	}()
	<-done
	if !allZero(value) {
		t.Fatal("Goexit path did not wipe the secret")
	}
}

func TestTakeSecretWipesRejectedAllocations(t *testing.T) {
	tests := []struct {
		name  string
		value []byte
	}{
		{name: "empty", value: make([]byte, 0, 32)},
		{name: "oversized capacity", value: bytes.Repeat([]byte{0x66}, MaxSecretBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fullAllocation := test.value[:cap(test.value)]
			if _, err := TakeSecret(test.value); !errors.Is(err, ErrInvalidSecretSize) {
				t.Fatalf("TakeSecret error = %v", err)
			}
			if !allZero(fullAllocation) {
				t.Fatal("rejected allocation was not wiped")
			}
		})
	}
}

func TestSecretCloseWaitsForInFlightUse(t *testing.T) {
	value := bytes.Repeat([]byte{0x77}, 32)
	secret, err := TakeSecret(value)
	if err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	useDone := make(chan struct{})
	go func() {
		defer close(useDone)
		if err := secret.Use(func([]byte) error {
			close(entered)
			<-release
			return nil
		}); err != nil {
			t.Errorf("Use error = %v", err)
		}
	}()
	<-entered

	closeStarted := make(chan struct{})
	closeDone := make(chan struct{})
	go func() {
		close(closeStarted)
		secret.Close()
		close(closeDone)
	}()
	<-closeStarted
	select {
	case <-closeDone:
		t.Fatal("Close returned while Use still held the secret")
	default:
	}
	close(release)
	<-useDone
	<-closeDone
	if !allZero(value) {
		t.Fatal("Close did not wipe after the in-flight callback returned")
	}
}

func TestSecretFormattingIsRedacted(t *testing.T) {
	seed := "seeded-secret-must-not-appear"
	value := []byte(seed)
	secret, err := TakeSecret(value)
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Close()

	for _, formatted := range []string{
		fmt.Sprintf("%v", secret),
		fmt.Sprintf("%+v", secret),
		fmt.Sprintf("%#v", secret),
	} {
		if strings.Contains(formatted, seed) || strings.Contains(formatted, "115 101 101 100") {
			t.Fatalf("formatted secret exposed bytes: %s", formatted)
		}
	}
}

func TestNilSecretIsClosed(t *testing.T) {
	var secret *Secret
	if secret.Len() != 0 {
		t.Fatalf("nil Len() = %d, want 0", secret.Len())
	}
	if err := secret.Use(func([]byte) error { return nil }); !errors.Is(err, ErrSecretClosed) {
		t.Fatalf("nil Use error = %v", err)
	}
	secret.Close()
	secret.Wipe()
}

func allZero(value []byte) bool {
	var combined byte
	for _, item := range value {
		combined |= item
	}
	return combined == 0
}

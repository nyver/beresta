package crypto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

func TestCalibrateArgon2idDeterministicChoices(t *testing.T) {
	tests := []struct {
		name           string
		initial        time.Duration
		candidate      time.Duration
		wantTimeCost   uint32
		wantBenchmarks int
	}{
		{name: "initial profile is on target", initial: time.Second, wantTimeCost: 3, wantBenchmarks: 1},
		{name: "slow device lowers time cost", initial: 6 * time.Second, candidate: 2 * time.Second, wantTimeCost: 1, wantBenchmarks: 2},
		{name: "fast device raises time cost", initial: 100 * time.Millisecond, candidate: time.Second, wantTimeCost: 30, wantBenchmarks: 2},
		{name: "time cost clamps to maximum", initial: time.Millisecond, candidate: 900 * time.Millisecond, wantTimeCost: 32, wantBenchmarks: 2},
		{name: "worse candidate keeps initial", initial: 1500 * time.Millisecond, candidate: 3 * time.Second, wantTimeCost: 3, wantBenchmarks: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			benchmark := func(_ context.Context, params Argon2idParams) (time.Duration, error) {
				calls++
				if calls == 1 {
					if params.TimeCost != InitialArgon2idTimeCost {
						t.Fatalf("initial time cost = %d", params.TimeCost)
					}
					return test.initial, nil
				}
				return test.candidate, nil
			}

			params, err := calibrateArgon2id(
				context.Background(),
				bytes.NewReader(bytes.Repeat([]byte{0x42}, Argon2idSaltBytes)),
				Argon2idCalibrationOptions{MemoryLimitKiB: 64 * 1024, Parallelism: 2},
				benchmark,
			)
			if err != nil {
				t.Fatal(err)
			}
			if params.TimeCost != test.wantTimeCost {
				t.Fatalf("time cost = %d, want %d", params.TimeCost, test.wantTimeCost)
			}
			if calls != test.wantBenchmarks {
				t.Fatalf("benchmark calls = %d, want %d", calls, test.wantBenchmarks)
			}
			if params.MemoryKiB != 64*1024 || params.Parallelism != 2 {
				t.Fatalf("device profile = %+v", params)
			}
			if !bytes.Equal(params.Salt, bytes.Repeat([]byte{0x42}, Argon2idSaltBytes)) {
				t.Fatal("calibration did not persist the generated salt")
			}
		})
	}
}

func TestCalibrateArgon2idEnforcesResourceBounds(t *testing.T) {
	benchmark := func(_ context.Context, params Argon2idParams) (time.Duration, error) {
		if params.MemoryKiB > MaxArgon2idMemoryKiB {
			t.Fatalf("memory = %d exceeds ceiling", params.MemoryKiB)
		}
		return Argon2idTargetDuration, nil
	}
	params, err := calibrateArgon2id(
		context.Background(),
		bytes.NewReader(make([]byte, Argon2idSaltBytes)),
		Argon2idCalibrationOptions{MemoryLimitKiB: MaxArgon2idMemoryKiB + 1, Parallelism: 1},
		benchmark,
	)
	if err != nil {
		t.Fatal(err)
	}
	if params.MemoryKiB != MaxArgon2idMemoryKiB {
		t.Fatalf("memory = %d, want %d", params.MemoryKiB, MaxArgon2idMemoryKiB)
	}

	for _, options := range []Argon2idCalibrationOptions{
		{MemoryLimitKiB: MinArgon2idMemoryKiB - 1, Parallelism: 1},
		{MemoryLimitKiB: MinArgon2idMemoryKiB, Parallelism: MaxArgon2idParallelism + 1},
	} {
		if _, err := calibrateArgon2id(context.Background(), bytes.NewReader(make([]byte, Argon2idSaltBytes)), options, benchmark); !errors.Is(err, ErrInvalidKDFParams) {
			t.Fatalf("calibration error = %v", err)
		}
	}
}

func TestCalibrateArgon2idCancellationAndFailures(t *testing.T) {
	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		called := false
		_, err := calibrateArgon2id(ctx, bytes.NewReader(make([]byte, Argon2idSaltBytes)), Argon2idCalibrationOptions{Parallelism: 1}, func(context.Context, Argon2idParams) (time.Duration, error) {
			called = true
			return time.Second, nil
		})
		if !errors.Is(err, context.Canceled) || called {
			t.Fatalf("error = %v, benchmark called = %t", err, called)
		}
	})

	t.Run("cancelled before candidate", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		_, err := calibrateArgon2id(ctx, bytes.NewReader(make([]byte, Argon2idSaltBytes)), Argon2idCalibrationOptions{Parallelism: 1}, func(context.Context, Argon2idParams) (time.Duration, error) {
			calls++
			cancel()
			return 100 * time.Millisecond, nil
		})
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("error = %v, calls = %d", err, calls)
		}
	})

	t.Run("benchmark reports cancellation", func(t *testing.T) {
		_, err := calibrateArgon2id(context.Background(), bytes.NewReader(make([]byte, Argon2idSaltBytes)), Argon2idCalibrationOptions{Parallelism: 1}, func(context.Context, Argon2idParams) (time.Duration, error) {
			return 0, context.DeadlineExceeded
		})
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrKDFCalibration) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("short random source", func(t *testing.T) {
		_, err := calibrateArgon2id(context.Background(), io.LimitReader(bytes.NewReader(make([]byte, Argon2idSaltBytes)), 1), Argon2idCalibrationOptions{Parallelism: 1}, func(context.Context, Argon2idParams) (time.Duration, error) {
			t.Fatal("benchmark called after random-source failure")
			return 0, nil
		})
		if !errors.Is(err, ErrKDFRandomSource) {
			t.Fatalf("error = %v", err)
		}
	})

	for _, test := range []struct {
		name      string
		benchmark argon2idBenchmark
	}{
		{name: "benchmark error", benchmark: func(context.Context, Argon2idParams) (time.Duration, error) { return 0, errors.New("timer failed") }},
		{name: "zero duration", benchmark: func(context.Context, Argon2idParams) (time.Duration, error) { return 0, nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := calibrateArgon2id(context.Background(), bytes.NewReader(make([]byte, Argon2idSaltBytes)), Argon2idCalibrationOptions{Parallelism: 1}, test.benchmark)
			if !errors.Is(err, ErrKDFCalibration) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestArgon2idProductionBenchmark(t *testing.T) {
	duration, err := benchmarkArgon2id(context.Background(), testArgon2idParams())
	if err != nil {
		t.Fatal(err)
	}
	if duration <= 0 {
		t.Fatalf("duration = %v", duration)
	}
}

func TestArgon2idParamsValidationAndPersistenceCopy(t *testing.T) {
	valid := testArgon2idParams()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Argon2idParams
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	if err := persisted.Validate(); err != nil {
		t.Fatalf("persisted profile is invalid: %v", err)
	}
	if !bytes.Equal(persisted.Salt, valid.Salt) {
		t.Fatal("persisted salt changed")
	}

	clone := valid.Clone()
	clone.Salt[0] ^= 0xff
	if bytes.Equal(clone.Salt, valid.Salt) {
		t.Fatal("Clone retained the source salt allocation")
	}

	tests := []Argon2idParams{
		withArgon2idParam(valid, func(p *Argon2idParams) { p.CryptoProfile = "future" }),
		withArgon2idParam(valid, func(p *Argon2idParams) { p.Algorithm = "argon2i" }),
		withArgon2idParam(valid, func(p *Argon2idParams) { p.Salt = p.Salt[:15] }),
		withArgon2idParam(valid, func(p *Argon2idParams) { p.MemoryKiB = MinArgon2idMemoryKiB - 1 }),
		withArgon2idParam(valid, func(p *Argon2idParams) { p.MemoryKiB = MaxArgon2idMemoryKiB + 1 }),
		withArgon2idParam(valid, func(p *Argon2idParams) { p.TimeCost = 0 }),
		withArgon2idParam(valid, func(p *Argon2idParams) { p.TimeCost = MaxArgon2idTimeCost + 1 }),
		withArgon2idParam(valid, func(p *Argon2idParams) { p.Parallelism = 0 }),
		withArgon2idParam(valid, func(p *Argon2idParams) { p.Parallelism = MaxArgon2idParallelism + 1 }),
		withArgon2idParam(valid, func(p *Argon2idParams) { p.DerivedKeyBytes = RootKeyBytes - 1 }),
	}
	for index, params := range tests {
		if err := params.Validate(); !errors.Is(err, ErrInvalidKDFParams) {
			t.Fatalf("case %d error = %v", index, err)
		}
	}
}

func TestDeriveRootKeyIsDeterministicAndOwned(t *testing.T) {
	params := testArgon2idParams()
	first := deriveRootKeyBytes(t, []byte("correct horse battery staple"), params)
	second := deriveRootKeyBytes(t, []byte("correct horse battery staple"), params.Clone())
	if !bytes.Equal(first, second) {
		t.Fatal("persisted parameters did not reproduce the Root Key")
	}
	different := deriveRootKeyBytes(t, []byte("different passphrase"), params)
	if bytes.Equal(first, different) {
		t.Fatal("different passphrases produced the same Root Key")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if secret, err := DeriveRootKey(ctx, []byte("passphrase"), params); !errors.Is(err, context.Canceled) || secret != nil {
		t.Fatalf("cancelled derivation = %v, %v", secret, err)
	}
	invalid := params
	invalid.MemoryKiB = MaxArgon2idMemoryKiB + 1
	if secret, err := DeriveRootKey(context.Background(), []byte("passphrase"), invalid); !errors.Is(err, ErrInvalidKDFParams) || secret != nil {
		t.Fatalf("invalid derivation = %v, %v", secret, err)
	}
}

func testArgon2idParams() Argon2idParams {
	return Argon2idParams{
		CryptoProfile:   CryptoProfileV1,
		Algorithm:       Argon2idName,
		Salt:            []byte("0123456789abcdef"),
		MemoryKiB:       MinArgon2idMemoryKiB,
		TimeCost:        1,
		Parallelism:     1,
		DerivedKeyBytes: RootKeyBytes,
	}
}

func withArgon2idParam(source Argon2idParams, mutate func(*Argon2idParams)) Argon2idParams {
	result := source.Clone()
	mutate(&result)
	return result
}

func deriveRootKeyBytes(t *testing.T, passphrase []byte, params Argon2idParams) []byte {
	t.Helper()
	secret, err := DeriveRootKey(context.Background(), passphrase, params)
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Close()
	if secret.Len() != RootKeyBytes {
		t.Fatalf("Root Key length = %d", secret.Len())
	}
	var result []byte
	if err := secret.Use(func(value []byte) error {
		result = append([]byte(nil), value...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

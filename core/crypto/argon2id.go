package crypto

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"runtime"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	CryptoProfileV1 = "beresta.crypto.v1"
	Argon2idName    = "argon2id"

	Argon2idSaltBytes       = 16
	RootKeyBytes            = 32
	MinArgon2idMemoryKiB    = 8 * 1024
	MaxArgon2idMemoryKiB    = 128 * 1024
	MinArgon2idTimeCost     = 1
	MaxArgon2idTimeCost     = 32
	MaxArgon2idParallelism  = 64
	InitialArgon2idTimeCost = 3

	Argon2idTargetDuration = time.Second
)

var (
	ErrInvalidKDFParams = errors.New("crypto: invalid KDF parameters")
	ErrKDFRandomSource  = errors.New("crypto: KDF random source failed")
	ErrKDFCalibration   = errors.New("crypto: KDF calibration failed")
)

// Argon2idParams is the validated KDF header persisted with a keybag or
// standalone backup. Container codecs encode Salt as a byte string.
type Argon2idParams struct {
	CryptoProfile   string `json:"crypto_profile" cbor:"crypto_profile"`
	Algorithm       string `json:"algorithm" cbor:"algorithm"`
	Salt            []byte `json:"salt" cbor:"salt"`
	MemoryKiB       uint32 `json:"memory_kib" cbor:"memory_kib"`
	TimeCost        uint32 `json:"time_cost" cbor:"time_cost"`
	Parallelism     uint32 `json:"parallelism" cbor:"parallelism"`
	DerivedKeyBytes uint32 `json:"derived_key_bytes" cbor:"derived_key_bytes"`
}

// Validate rejects unsupported versions and unsafe resource parameters before
// Argon2 allocates memory.
func (p Argon2idParams) Validate() error {
	switch {
	case p.CryptoProfile != CryptoProfileV1:
		return fmt.Errorf("%w: unsupported crypto profile", ErrInvalidKDFParams)
	case p.Algorithm != Argon2idName:
		return fmt.Errorf("%w: unsupported algorithm", ErrInvalidKDFParams)
	case len(p.Salt) != Argon2idSaltBytes:
		return fmt.Errorf("%w: salt must be %d bytes", ErrInvalidKDFParams, Argon2idSaltBytes)
	case p.MemoryKiB < MinArgon2idMemoryKiB || p.MemoryKiB > MaxArgon2idMemoryKiB:
		return fmt.Errorf("%w: memory must be between %d and %d KiB", ErrInvalidKDFParams, MinArgon2idMemoryKiB, MaxArgon2idMemoryKiB)
	case p.TimeCost < MinArgon2idTimeCost || p.TimeCost > MaxArgon2idTimeCost:
		return fmt.Errorf("%w: time cost must be between %d and %d", ErrInvalidKDFParams, MinArgon2idTimeCost, MaxArgon2idTimeCost)
	case p.Parallelism == 0 || p.Parallelism > MaxArgon2idParallelism:
		return fmt.Errorf("%w: parallelism must be between 1 and %d", ErrInvalidKDFParams, MaxArgon2idParallelism)
	case p.DerivedKeyBytes != RootKeyBytes:
		return fmt.Errorf("%w: derived key length must be %d bytes", ErrInvalidKDFParams, RootKeyBytes)
	default:
		return nil
	}
}

// Clone returns an independently owned persistence value.
func (p Argon2idParams) Clone() Argon2idParams {
	p.Salt = append([]byte(nil), p.Salt...)
	return p
}

// Argon2idCalibrationOptions describes platform-safe device limits. A zero
// memory limit selects the profile ceiling; values above it are clamped. A zero
// parallelism value selects up to four useful logical CPUs.
type Argon2idCalibrationOptions struct {
	MemoryLimitKiB uint32
	Parallelism    uint32
}

type argon2idBenchmark func(context.Context, Argon2idParams) (time.Duration, error)

// CalibrateArgon2id creates the persisted profile and calibrates its time cost
// toward one second. Context cancellation is honored before each non-cancellable
// Argon2 invocation.
func CalibrateArgon2id(ctx context.Context, options Argon2idCalibrationOptions) (Argon2idParams, error) {
	return calibrateArgon2id(ctx, cryptorand.Reader, options, benchmarkArgon2id)
}

func calibrateArgon2id(ctx context.Context, random io.Reader, options Argon2idCalibrationOptions, benchmark argon2idBenchmark) (Argon2idParams, error) {
	if err := contextError(ctx); err != nil {
		return Argon2idParams{}, err
	}
	if random == nil || benchmark == nil {
		return Argon2idParams{}, ErrKDFCalibration
	}

	memoryKiB := options.MemoryLimitKiB
	if memoryKiB == 0 || memoryKiB > MaxArgon2idMemoryKiB {
		memoryKiB = MaxArgon2idMemoryKiB
	}
	if memoryKiB < MinArgon2idMemoryKiB {
		return Argon2idParams{}, fmt.Errorf("%w: device memory limit is below %d KiB", ErrInvalidKDFParams, MinArgon2idMemoryKiB)
	}

	parallelism := options.Parallelism
	if parallelism == 0 {
		parallelism = usefulParallelism()
	}
	if parallelism > MaxArgon2idParallelism {
		return Argon2idParams{}, fmt.Errorf("%w: device parallelism exceeds %d", ErrInvalidKDFParams, MaxArgon2idParallelism)
	}

	params := Argon2idParams{
		CryptoProfile:   CryptoProfileV1,
		Algorithm:       Argon2idName,
		Salt:            make([]byte, Argon2idSaltBytes),
		MemoryKiB:       memoryKiB,
		TimeCost:        InitialArgon2idTimeCost,
		Parallelism:     parallelism,
		DerivedKeyBytes: RootKeyBytes,
	}
	if _, err := io.ReadFull(random, params.Salt); err != nil {
		wipe(params.Salt)
		return Argon2idParams{}, fmt.Errorf("%w: %v", ErrKDFRandomSource, err)
	}

	initialDuration, err := runArgon2idBenchmark(ctx, benchmark, params)
	if err != nil {
		wipe(params.Salt)
		return Argon2idParams{}, err
	}
	candidateTimeCost := scaledArgon2idTimeCost(params.TimeCost, initialDuration)
	if candidateTimeCost != params.TimeCost {
		candidate := params
		candidate.TimeCost = candidateTimeCost
		candidateDuration, candidateErr := runArgon2idBenchmark(ctx, benchmark, candidate)
		if candidateErr != nil {
			wipe(params.Salt)
			return Argon2idParams{}, candidateErr
		}
		if durationDistance(candidateDuration, Argon2idTargetDuration) < durationDistance(initialDuration, Argon2idTargetDuration) {
			params.TimeCost = candidateTimeCost
		}
	}

	if err := params.Validate(); err != nil {
		wipe(params.Salt)
		return Argon2idParams{}, err
	}
	return params, nil
}

// DeriveRootKey validates persisted parameters, checks cancellation, and
// returns the 32-byte Root Key in an owned mutable Secret. Argon2 itself cannot
// be interrupted after it starts.
func DeriveRootKey(ctx context.Context, passphrase []byte, params Argon2idParams) (*Secret, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}

	rootKey := argon2.IDKey(
		passphrase,
		params.Salt,
		params.TimeCost,
		params.MemoryKiB,
		uint8(params.Parallelism),
		params.DerivedKeyBytes,
	)
	secret, err := TakeSecret(rootKey)
	if err != nil {
		wipe(rootKey)
		return nil, err
	}
	return secret, nil
}

func runArgon2idBenchmark(ctx context.Context, benchmark argon2idBenchmark, params Argon2idParams) (time.Duration, error) {
	if err := contextError(ctx); err != nil {
		return 0, err
	}
	duration, err := benchmark(ctx, params)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, err
		}
		return 0, fmt.Errorf("%w: %v", ErrKDFCalibration, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%w: benchmark returned a non-positive duration", ErrKDFCalibration)
	}
	return duration, nil
}

func benchmarkArgon2id(ctx context.Context, params Argon2idParams) (time.Duration, error) {
	if err := contextError(ctx); err != nil {
		return 0, err
	}
	started := time.Now()
	calibrationKey := argon2.IDKey(
		[]byte("beresta-argon2id-calibration-v1"),
		params.Salt,
		params.TimeCost,
		params.MemoryKiB,
		uint8(params.Parallelism),
		params.DerivedKeyBytes,
	)
	duration := time.Since(started)
	wipe(calibrationKey)
	return duration, nil
}

func scaledArgon2idTimeCost(current uint32, measured time.Duration) uint32 {
	numerator := uint64(current)*uint64(Argon2idTargetDuration) + uint64(measured)/2
	candidate := numerator / uint64(measured)
	if candidate < MinArgon2idTimeCost {
		return MinArgon2idTimeCost
	}
	if candidate > MaxArgon2idTimeCost {
		return MaxArgon2idTimeCost
	}
	return uint32(candidate)
}

func durationDistance(value, target time.Duration) time.Duration {
	if value > target {
		return value - target
	}
	return target - value
}

func usefulParallelism() uint32 {
	logicalCPUs := runtime.NumCPU()
	if schedulerCPUs := runtime.GOMAXPROCS(0); schedulerCPUs < logicalCPUs {
		logicalCPUs = schedulerCPUs
	}
	if logicalCPUs < 1 {
		return 1
	}
	if logicalCPUs > 4 {
		return 4
	}
	return uint32(logicalCPUs)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

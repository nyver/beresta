package model

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// DefaultMaxFutureSkew bounds how far a received remote clock may lead the
// local wall clock before its physical component is clamped.
const DefaultMaxFutureSkew = 5 * time.Minute

var (
	// ErrInvalidHLC reports a structurally invalid Hybrid Logical Clock value.
	ErrInvalidHLC = errors.New("model: invalid Hybrid Logical Clock value")
	// ErrClockOverflow reports that the logical counter is exhausted at the
	// current physical time; the caller must wait for physical time to advance.
	ErrClockOverflow = errors.New("model: Hybrid Logical Clock logical counter overflow")
)

// HLC is a Hybrid Logical Clock value: schema `beresta.hlc.v1`.
type HLC struct {
	PhysicalMS uint64
	Logical    uint32
	DeviceID   ID
}

// Validate rejects a structurally invalid clock value. The zero value is
// valid: it represents "never written" for a register that has not yet
// received a value.
func (h HLC) Validate() error {
	if h.IsZero() {
		return nil
	}
	if err := h.DeviceID.Validate(); err != nil {
		return fmt.Errorf("%w: device ID", ErrInvalidHLC)
	}
	return nil
}

// IsZero reports whether h is the zero "never written" value.
func (h HLC) IsZero() bool {
	return h.PhysicalMS == 0 && h.Logical == 0 && h.DeviceID.IsZero()
}

// Compare orders two clock values lexicographically by (physical_ms,
// logical, device_id bytes), the fixed deterministic tie break required by
// the synchronization protocol for LWW registers.
func (h HLC) Compare(other HLC) int {
	switch {
	case h.PhysicalMS < other.PhysicalMS:
		return -1
	case h.PhysicalMS > other.PhysicalMS:
		return 1
	}
	switch {
	case h.Logical < other.Logical:
		return -1
	case h.Logical > other.Logical:
		return 1
	}
	return h.DeviceID.Compare(other.DeviceID)
}

// Clock generates and merges HLC values for one device. Callers must persist
// the value returned by Tick or Observe, and restore it through NewClock, so
// the clock never regresses across a restart.
type Clock struct {
	mu            sync.Mutex
	deviceID      ID
	last          HLC
	maxFutureSkew time.Duration
	now           func() time.Time
}

// NewClock restores a device clock from its last persisted value. A zero
// persisted value starts a fresh clock. A zero maxFutureSkew selects
// DefaultMaxFutureSkew.
func NewClock(deviceID ID, persisted HLC, maxFutureSkew time.Duration) (*Clock, error) {
	return newClock(deviceID, persisted, maxFutureSkew, time.Now)
}

func newClock(deviceID ID, persisted HLC, maxFutureSkew time.Duration, now func() time.Time) (*Clock, error) {
	if err := deviceID.Validate(); err != nil {
		return nil, fmt.Errorf("%w: device ID", ErrInvalidHLC)
	}
	if err := persisted.Validate(); err != nil {
		return nil, err
	}
	if maxFutureSkew <= 0 {
		maxFutureSkew = DefaultMaxFutureSkew
	}
	if now == nil {
		now = time.Now
	}
	return &Clock{
		deviceID:      deviceID,
		last:          persisted,
		maxFutureSkew: maxFutureSkew,
		now:           now,
	}, nil
}

// Last returns the most recently issued or merged clock value.
func (c *Clock) Last() HLC {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Tick issues a new local-event clock value strictly after every previously
// issued or observed value. The caller must persist the result before
// exposing any operation signed with it.
func (c *Clock) Tick() (HLC, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	nowMS := uint64(c.now().UnixMilli())
	next, err := c.last.advance(nowMS, c.deviceID)
	if err != nil {
		return HLC{}, err
	}
	c.last = next
	return next, nil
}

// Observe merges a validated remote clock value into the local clock,
// clamping a remote physical component that leads the local wall clock by
// more than the configured maximum future skew. The caller must persist the
// result before emitting any operation causally dependent on it.
func (c *Clock) Observe(remote HLC) (HLC, error) {
	if err := remote.Validate(); err != nil {
		return HLC{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	nowMS := uint64(c.now().UnixMilli())
	maxAllowed := nowMS + uint64(c.maxFutureSkew.Milliseconds())
	remotePhysical := remote.PhysicalMS
	remoteLogical := remote.Logical
	if remotePhysical > maxAllowed {
		// The remote physical time is clamped to the allowed window; its
		// logical counter no longer applies to the clamped instant.
		remotePhysical = maxAllowed
		remoteLogical = 0
	}

	physical := c.last.PhysicalMS
	if remotePhysical > physical {
		physical = remotePhysical
	}
	if nowMS > physical {
		physical = nowMS
	}

	var logical uint32
	switch {
	case physical == c.last.PhysicalMS && physical == remotePhysical:
		logical = c.last.Logical
		if remoteLogical > logical {
			logical = remoteLogical
		}
		if logical == math.MaxUint32 {
			return HLC{}, ErrClockOverflow
		}
		logical++
	case physical == c.last.PhysicalMS:
		if c.last.Logical == math.MaxUint32 {
			return HLC{}, ErrClockOverflow
		}
		logical = c.last.Logical + 1
	case physical == remotePhysical:
		if remoteLogical == math.MaxUint32 {
			return HLC{}, ErrClockOverflow
		}
		logical = remoteLogical + 1
	default:
		logical = 0
	}

	next := HLC{PhysicalMS: physical, Logical: logical, DeviceID: c.deviceID}
	c.last = next
	return next, nil
}

// advance produces the next local-event value after h at wall-clock time
// nowMS. The logical counter increments only when physical time has not
// advanced, and overflowing it requires the caller to wait for physical time
// to advance before a new event can be issued.
func (h HLC) advance(nowMS uint64, deviceID ID) (HLC, error) {
	if nowMS > h.PhysicalMS {
		return HLC{PhysicalMS: nowMS, Logical: 0, DeviceID: deviceID}, nil
	}
	if h.Logical == math.MaxUint32 {
		return HLC{}, ErrClockOverflow
	}
	return HLC{PhysicalMS: h.PhysicalMS, Logical: h.Logical + 1, DeviceID: deviceID}, nil
}

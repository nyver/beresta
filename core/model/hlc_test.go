package model

import (
	"errors"
	"math"
	"testing"
	"time"
)

func testDeviceID(t *testing.T, seed byte) ID {
	t.Helper()
	var id ID
	for i := range id {
		id[i] = seed
	}
	id[6] = (id[6] & 0x0f) | 0x70
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func TestHLCCompareOrdering(t *testing.T) {
	deviceLow := testDeviceID(t, 0x01)
	deviceHigh := testDeviceID(t, 0x02)

	tests := []struct {
		name string
		a, b HLC
		want int
	}{
		{"earlier physical", HLC{PhysicalMS: 1, DeviceID: deviceLow}, HLC{PhysicalMS: 2, DeviceID: deviceLow}, -1},
		{"later physical", HLC{PhysicalMS: 2, DeviceID: deviceLow}, HLC{PhysicalMS: 1, DeviceID: deviceLow}, 1},
		{"equal physical, logical breaks tie", HLC{PhysicalMS: 5, Logical: 1, DeviceID: deviceLow}, HLC{PhysicalMS: 5, Logical: 2, DeviceID: deviceLow}, -1},
		{"equal physical and logical, device breaks tie", HLC{PhysicalMS: 5, Logical: 1, DeviceID: deviceLow}, HLC{PhysicalMS: 5, Logical: 1, DeviceID: deviceHigh}, -1},
		{"fully equal", HLC{PhysicalMS: 5, Logical: 1, DeviceID: deviceLow}, HLC{PhysicalMS: 5, Logical: 1, DeviceID: deviceLow}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Compare(tt.b); got != tt.want {
				t.Fatalf("Compare() = %d, want %d", got, tt.want)
			}
			if got := tt.b.Compare(tt.a); got != -tt.want {
				t.Fatalf("reverse Compare() = %d, want %d", got, -tt.want)
			}
		})
	}
}

func TestHLCValidate(t *testing.T) {
	if err := (HLC{}).Validate(); err != nil {
		t.Fatalf("zero value Validate() error = %v, want nil", err)
	}
	invalid := HLC{PhysicalMS: 1, DeviceID: ID{}}
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidHLC) {
		t.Fatalf("Validate() error = %v, want ErrInvalidHLC", err)
	}
}

func TestClockTickAdvancesPhysicalTime(t *testing.T) {
	device := testDeviceID(t, 0x03)
	tick := time.UnixMilli(1000)
	clock, err := newClock(device, HLC{}, 0, func() time.Time { return tick })
	if err != nil {
		t.Fatal(err)
	}

	first, err := clock.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if first.PhysicalMS != 1000 || first.Logical != 0 || first.DeviceID != device {
		t.Fatalf("first tick = %+v", first)
	}

	second, err := clock.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if second.PhysicalMS != 1000 || second.Logical != 1 {
		t.Fatalf("second tick (same ms) = %+v, want logical=1", second)
	}

	tick = time.UnixMilli(1001)
	third, err := clock.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if third.PhysicalMS != 1001 || third.Logical != 0 {
		t.Fatalf("third tick (advanced ms) = %+v, want logical reset to 0", third)
	}
}

func TestClockTickOverflowRequiresPhysicalAdvance(t *testing.T) {
	device := testDeviceID(t, 0x04)
	tick := time.UnixMilli(1000)
	clock, err := newClock(device, HLC{PhysicalMS: 1000, Logical: math.MaxUint32, DeviceID: device}, 0, func() time.Time { return tick })
	if err != nil {
		t.Fatal(err)
	}

	if _, err := clock.Tick(); !errors.Is(err, ErrClockOverflow) {
		t.Fatalf("Tick() error = %v, want ErrClockOverflow", err)
	}

	tick = time.UnixMilli(1001)
	next, err := clock.Tick()
	if err != nil {
		t.Fatalf("Tick() after time advance error = %v", err)
	}
	if next.PhysicalMS != 1001 || next.Logical != 0 {
		t.Fatalf("Tick() after overflow recovery = %+v", next)
	}
}

func TestClockObserveClampsFutureSkew(t *testing.T) {
	device := testDeviceID(t, 0x05)
	remoteDevice := testDeviceID(t, 0x06)
	now := time.UnixMilli(1_000_000)
	skew := 5 * time.Minute
	clock, err := newClock(device, HLC{}, skew, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	farFuture := HLC{PhysicalMS: uint64(now.UnixMilli()) + uint64(time.Hour.Milliseconds()), Logical: 7, DeviceID: remoteDevice}
	merged, err := clock.Observe(farFuture)
	if err != nil {
		t.Fatal(err)
	}
	wantMax := uint64(now.UnixMilli()) + uint64(skew.Milliseconds())
	if merged.PhysicalMS != wantMax {
		t.Fatalf("Observe() PhysicalMS = %d, want clamp to %d", merged.PhysicalMS, wantMax)
	}
	if merged.DeviceID != device {
		t.Fatalf("Observe() DeviceID = %s, want local device %s", merged.DeviceID, device)
	}
}

func TestClockObserveMergesLogicalOnEqualPhysical(t *testing.T) {
	device := testDeviceID(t, 0x07)
	remoteDevice := testDeviceID(t, 0x08)
	now := time.UnixMilli(2000)
	clock, err := newClock(device, HLC{PhysicalMS: 2000, Logical: 3, DeviceID: device}, 0, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	remote := HLC{PhysicalMS: 2000, Logical: 9, DeviceID: remoteDevice}
	merged, err := clock.Observe(remote)
	if err != nil {
		t.Fatal(err)
	}
	if merged.PhysicalMS != 2000 || merged.Logical != 10 {
		t.Fatalf("Observe() = %+v, want physical=2000 logical=10", merged)
	}
}

func TestClockObserveRejectsInvalidRemote(t *testing.T) {
	device := testDeviceID(t, 0x09)
	clock, err := newClock(device, HLC{}, 0, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	invalid := HLC{PhysicalMS: 1, DeviceID: ID{}}
	if _, err := clock.Observe(invalid); !errors.Is(err, ErrInvalidHLC) {
		t.Fatalf("Observe() error = %v, want ErrInvalidHLC", err)
	}
}

func TestNewClockRejectsInvalidInputs(t *testing.T) {
	device := testDeviceID(t, 0x0a)
	if _, err := NewClock(ID{}, HLC{}, 0); !errors.Is(err, ErrInvalidHLC) {
		t.Fatalf("NewClock() with invalid device error = %v, want ErrInvalidHLC", err)
	}
	if _, err := NewClock(device, HLC{PhysicalMS: 1, DeviceID: ID{}}, 0); !errors.Is(err, ErrInvalidHLC) {
		t.Fatalf("NewClock() with invalid persisted clock error = %v, want ErrInvalidHLC", err)
	}
	if _, err := NewClock(device, HLC{}, 0); err != nil {
		t.Fatalf("NewClock() with zero persisted clock error = %v, want nil", err)
	}
}

func TestClockPersistedStateSurvivesRestart(t *testing.T) {
	device := testDeviceID(t, 0x0b)
	now := time.UnixMilli(5000)
	clock, err := newClock(device, HLC{}, 0, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	issued, err := clock.Tick()
	if err != nil {
		t.Fatal(err)
	}

	restored, err := newClock(device, issued, 0, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	next, err := restored.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if next.Logical != issued.Logical+1 {
		t.Fatalf("Tick() after restore = %+v, want logical=%d", next, issued.Logical+1)
	}
}

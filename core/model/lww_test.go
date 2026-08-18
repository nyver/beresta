package model

import "testing"

func TestLWWMergePicksLaterClock(t *testing.T) {
	device := testDeviceID(t, 0x21)
	earlier := LWW[string]{Value: "old title", Clock: HLC{PhysicalMS: 1, DeviceID: device}}
	later := LWW[string]{Value: "new title", Clock: HLC{PhysicalMS: 2, DeviceID: device}}

	if got := earlier.Merge(later); got != later {
		t.Fatalf("earlier.Merge(later) = %+v, want %+v", got, later)
	}
	if got := later.Merge(earlier); got != later {
		t.Fatalf("later.Merge(earlier) = %+v, want %+v", got, later)
	}
}

func TestLWWMergeTieBreaksByDeviceID(t *testing.T) {
	low := testDeviceID(t, 0x22)
	high := testDeviceID(t, 0x23)

	fromLow := LWW[bool]{Value: false, Clock: HLC{PhysicalMS: 5, Logical: 1, DeviceID: low}}
	fromHigh := LWW[bool]{Value: true, Clock: HLC{PhysicalMS: 5, Logical: 1, DeviceID: high}}

	if got := fromLow.Merge(fromHigh); got != fromHigh {
		t.Fatalf("Merge() = %+v, want the higher device ID to win deterministically", got)
	}
	if got := fromHigh.Merge(fromLow); got != fromHigh {
		t.Fatalf("Merge() = %+v, want the higher device ID to win regardless of receiver", got)
	}
}

func TestLWWMergeKeepsReceiverOnIdenticalClock(t *testing.T) {
	device := testDeviceID(t, 0x24)
	value := LWW[int]{Value: 7, Clock: HLC{PhysicalMS: 5, Logical: 1, DeviceID: device}}
	duplicate := LWW[int]{Value: 7, Clock: HLC{PhysicalMS: 5, Logical: 1, DeviceID: device}}

	if got := value.Merge(duplicate); got != value {
		t.Fatalf("Merge() = %+v, want receiver kept on identical clock", got)
	}
}

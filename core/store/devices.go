package store

import (
	"context"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

// LoadDeviceClock returns the highest Hybrid Logical Clock value deviceID has
// persisted, so a restarted core/model.Clock can resume without regressing.
// A device with no recorded clock activity returns the zero HLC.
func LoadDeviceClock(ctx context.Context, exec Executor, deviceID model.ID) (model.HLC, error) {
	var physicalMS uint64
	var logical uint32
	err := exec.QueryRowContext(ctx,
		`SELECT clock_physical_ms, clock_logical FROM devices WHERE id = ?`, deviceID.Bytes(),
	).Scan(&physicalMS, &logical)
	if err != nil {
		return model.HLC{}, fmt.Errorf("store: load device clock: %w", err)
	}
	if physicalMS == 0 && logical == 0 {
		return model.HLC{}, nil
	}
	return model.HLC{PhysicalMS: physicalMS, Logical: logical, DeviceID: deviceID}, nil
}

// AdvanceDeviceClock persists clock as deviceID's highest issued HLC value if
// it is strictly greater than what is already stored. An older or equal
// value is silently ignored rather than an error, matching the LWW registers
// elsewhere in this package: callers only ever pass a value their own
// core/model.Clock just issued, so this only guards against out-of-order
// transaction commits, not conflicting writers.
func AdvanceDeviceClock(ctx context.Context, exec Executor, deviceID model.ID, clock model.HLC) error {
	_, err := exec.ExecContext(ctx,
		`UPDATE devices SET clock_physical_ms = ?, clock_logical = ? WHERE id = ?
		 AND (clock_physical_ms < ? OR (clock_physical_ms = ? AND clock_logical < ?))`,
		clock.PhysicalMS, clock.Logical, deviceID.Bytes(),
		clock.PhysicalMS, clock.PhysicalMS, clock.Logical,
	)
	if err != nil {
		return fmt.Errorf("store: advance device clock: %w", err)
	}
	return nil
}

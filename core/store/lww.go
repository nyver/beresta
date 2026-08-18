package store

import (
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

// lwwWhereClause returns a SQL boolean fragment that is true only when the
// six placeholders it expects (new physical, new physical, new logical, new
// physical, new logical, new device_id — see lwwArgs) describe a clock later
// than the row's existing physical/logical/device_id columns. A NULL
// existing physical column (register never written) always loses, so a
// first write always applies.
func lwwWhereClause(physicalCol, logicalCol, deviceCol string) string {
	return fmt.Sprintf(
		`(%[1]s IS NULL OR ? > %[1]s OR (? = %[1]s AND ? > %[2]s) OR (? = %[1]s AND ? = %[2]s AND ? > %[3]s))`,
		physicalCol, logicalCol, deviceCol,
	)
}

// lwwArgs returns clock's fields in the repeated order lwwWhereClause's
// placeholders expect.
func lwwArgs(clock model.HLC) []any {
	return []any{clock.PhysicalMS, clock.PhysicalMS, clock.Logical, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes()}
}

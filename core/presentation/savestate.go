package presentation

// LocalSaveState is the closed set of note-persistence states a UI may
// display, per the shared terminology requirement in
// specs/product-experience: note persistence is represented only as
// Saving, Saved, or CouldNotSave. Saved means a durable local commit; it
// never depends on synchronization reaching a server.
type LocalSaveState string

const (
	// SaveStateSaving means a local commit for the latest editor
	// generation is in flight.
	SaveStateSaving LocalSaveState = "saving"
	// SaveStateSaved means the latest editor generation has a durable
	// local commit receipt.
	SaveStateSaved LocalSaveState = "saved"
	// SaveStateCouldNotSave means the latest local commit attempt failed
	// and the dirty buffer is retained for retry.
	SaveStateCouldNotSave LocalSaveState = "could_not_save"
)

// Valid reports whether s is one of the closed LocalSaveState values.
func (s LocalSaveState) Valid() bool {
	switch s {
	case SaveStateSaving, SaveStateSaved, SaveStateCouldNotSave:
		return true
	default:
		return false
	}
}

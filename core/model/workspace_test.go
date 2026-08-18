package model

import (
	"errors"
	"testing"
)

func validWorkspace(t *testing.T) Workspace {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	return Workspace{ID: id, CreatedAt: HLC{PhysicalMS: 1, DeviceID: testDeviceID(t, 0x32)}}
}

func TestWorkspaceValidate(t *testing.T) {
	if err := validWorkspace(t).Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}

	tests := []struct {
		name   string
		modify func(*Workspace)
	}{
		{"zero ID", func(w *Workspace) { w.ID = Nil }},
		{"zero created clock", func(w *Workspace) { w.CreatedAt = HLC{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := validWorkspace(t)
			tt.modify(&w)
			if err := w.Validate(); !errors.Is(err, ErrInvalidWorkspace) {
				t.Fatalf("Validate() error = %v, want ErrInvalidWorkspace", err)
			}
		})
	}
}

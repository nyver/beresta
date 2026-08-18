package model

import (
	"bytes"
	"errors"
	"testing"
)

func validDevice(t *testing.T) Device {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	return Device{
		ID:        id,
		AccountID: accountID,
		PublicKey: bytes.Repeat([]byte{0x11}, Ed25519PublicKeyBytes),
		Status:    DeviceStatusActive,
		CreatedAt: HLC{PhysicalMS: 1, DeviceID: testDeviceID(t, 0x31)},
	}
}

func TestDeviceValidate(t *testing.T) {
	if err := validDevice(t).Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}

	tests := []struct {
		name   string
		modify func(*Device)
	}{
		{"zero ID", func(d *Device) { d.ID = Nil }},
		{"zero account ID", func(d *Device) { d.AccountID = Nil }},
		{"short public key", func(d *Device) { d.PublicKey = d.PublicKey[:16] }},
		{"unknown status", func(d *Device) { d.Status = DeviceStatus(0) }},
		{"zero created clock", func(d *Device) { d.CreatedAt = HLC{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDevice(t)
			tt.modify(&d)
			if err := d.Validate(); !errors.Is(err, ErrInvalidDevice) {
				t.Fatalf("Validate() error = %v, want ErrInvalidDevice", err)
			}
		})
	}
}

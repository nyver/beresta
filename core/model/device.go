package model

import (
	"errors"
	"fmt"
)

// Ed25519PublicKeyBytes is the fixed device signing key size shared with the
// v1 crypto profile (core/crypto.Ed25519PublicKeyBytes).
const Ed25519PublicKeyBytes = 32

// ErrInvalidDevice reports a structurally invalid device record.
var ErrInvalidDevice = errors.New("model: invalid device")

// DeviceStatus is the local view of a device's authorization state.
type DeviceStatus uint8

const (
	// DeviceStatusActive marks a device permitted to authenticate and submit
	// operations.
	DeviceStatusActive DeviceStatus = iota + 1
	// DeviceStatusRevoked marks a device that must be rejected by the server
	// and treated as untrusted going forward.
	DeviceStatusRevoked
)

func (s DeviceStatus) valid() bool {
	return s == DeviceStatusActive || s == DeviceStatusRevoked
}

// Device is one authorized signing identity for an account. Device private
// keys are never synchronized; only this public record is shared so a
// device can be revoked without invalidating any other device.
type Device struct {
	ID        ID
	AccountID ID
	PublicKey []byte // Ed25519 public key
	Status    DeviceStatus
	CreatedAt HLC
}

// Validate rejects a structurally invalid device record.
func (d Device) Validate() error {
	if err := d.ID.Validate(); err != nil {
		return fmt.Errorf("%w: ID", ErrInvalidDevice)
	}
	if err := d.AccountID.Validate(); err != nil {
		return fmt.Errorf("%w: account ID", ErrInvalidDevice)
	}
	if len(d.PublicKey) != Ed25519PublicKeyBytes {
		return fmt.Errorf("%w: public key", ErrInvalidDevice)
	}
	if !d.Status.valid() {
		return fmt.Errorf("%w: status", ErrInvalidDevice)
	}
	if err := d.CreatedAt.Validate(); err != nil || d.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created clock", ErrInvalidDevice)
	}
	return nil
}

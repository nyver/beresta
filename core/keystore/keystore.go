// Package keystore defines the platform-neutral contract for wrapping local
// device keys with operating-system key protection.
package keystore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
)

const (
	FormatVersion   = 1
	MaxIdentifier   = 128
	MaxWrappedBytes = 64 << 10
	headerBytes     = 14
)

var (
	ErrInvalidMetadata = errors.New("keystore: invalid metadata")
	ErrInvalidEnvelope = errors.New("keystore: invalid wrapped-key envelope")
	ErrUnavailable     = errors.New("keystore: platform protection unavailable")
	ErrAuthentication  = errors.New("keystore: user authentication failed")
	ErrCanceled        = errors.New("keystore: operation canceled")
	ErrKeyInvalidated  = errors.New("keystore: platform key invalidated")
)

var envelopeMagic = [4]byte{'B', 'K', 'W', '1'}

// Protection identifies the OS-enforced mechanism represented by an envelope.
type Protection uint8

const (
	ProtectionWindowsDPAPI Protection = iota + 1
	ProtectionWindowsHello
	ProtectionAndroidKeystore
	ProtectionAndroidBiometric
)

func (p Protection) String() string {
	switch p {
	case ProtectionWindowsDPAPI:
		return "windows-dpapi"
	case ProtectionWindowsHello:
		return "windows-hello"
	case ProtectionAndroidKeystore:
		return "android-keystore"
	case ProtectionAndroidBiometric:
		return "android-biometric"
	default:
		return "unknown"
	}
}

func (p Protection) valid() bool {
	return p >= ProtectionWindowsDPAPI && p <= ProtectionAndroidBiometric
}

// Metadata binds a wrapped key to its stable local identifier and purpose.
// Both fields are non-secret ASCII tokens and may be logged.
type Metadata struct {
	KeyID   string
	Purpose string
}

// Validate rejects ambiguous, empty, or excessively large metadata tokens.
func (m Metadata) Validate() error {
	if !validToken(m.KeyID) || !validToken(m.Purpose) {
		return ErrInvalidMetadata
	}
	return nil
}

func validToken(value string) bool {
	if len(value) == 0 || len(value) > MaxIdentifier {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}

// Wrapper is implemented by thin platform adapters. Implementations must bind
// Metadata to the OS operation and return owned Secret values from Unwrap.
type Wrapper interface {
	Protection() Protection
	Wrap(context.Context, Metadata, *corecrypto.Secret) ([]byte, error)
	Unwrap(context.Context, Metadata, []byte) (*corecrypto.Secret, error)
	Delete(context.Context, Metadata) error
}

// SealEnvelope serializes one platform ciphertext with canonical metadata.
func SealEnvelope(protection Protection, metadata Metadata, wrapped []byte) ([]byte, error) {
	if !protection.valid() || metadata.Validate() != nil || len(wrapped) == 0 || len(wrapped) > MaxWrappedBytes {
		return nil, ErrInvalidEnvelope
	}

	result := make([]byte, headerBytes+len(metadata.KeyID)+len(metadata.Purpose)+len(wrapped))
	copy(result[:4], envelopeMagic[:])
	result[4] = FormatVersion
	result[5] = byte(protection)
	binary.BigEndian.PutUint16(result[6:8], uint16(len(metadata.KeyID)))
	binary.BigEndian.PutUint16(result[8:10], uint16(len(metadata.Purpose)))
	binary.BigEndian.PutUint32(result[10:14], uint32(len(wrapped)))
	offset := headerBytes
	offset += copy(result[offset:], metadata.KeyID)
	offset += copy(result[offset:], metadata.Purpose)
	copy(result[offset:], wrapped)
	return result, nil
}

// OpenEnvelope validates a canonical envelope and returns a copy of its OS
// ciphertext. Metadata or protection substitution is reported uniformly.
func OpenEnvelope(encoded []byte, protection Protection, metadata Metadata) ([]byte, error) {
	if !protection.valid() || metadata.Validate() != nil || len(encoded) < headerBytes ||
		!bytes.Equal(encoded[:4], envelopeMagic[:]) || encoded[4] != FormatVersion ||
		Protection(encoded[5]) != protection {
		return nil, ErrInvalidEnvelope
	}
	keyLength := int(binary.BigEndian.Uint16(encoded[6:8]))
	purposeLength := int(binary.BigEndian.Uint16(encoded[8:10]))
	wrappedLength := int(binary.BigEndian.Uint32(encoded[10:14]))
	if keyLength == 0 || keyLength > MaxIdentifier || purposeLength == 0 || purposeLength > MaxIdentifier ||
		wrappedLength == 0 || wrappedLength > MaxWrappedBytes ||
		headerBytes+keyLength+purposeLength+wrappedLength != len(encoded) {
		return nil, ErrInvalidEnvelope
	}
	offset := headerBytes
	keyID := encoded[offset : offset+keyLength]
	offset += keyLength
	purpose := encoded[offset : offset+purposeLength]
	offset += purposeLength
	if !bytes.Equal(keyID, []byte(metadata.KeyID)) || !bytes.Equal(purpose, []byte(metadata.Purpose)) {
		return nil, ErrInvalidEnvelope
	}
	return bytes.Clone(encoded[offset:]), nil
}

// Binding returns the canonical, length-delimited public context supplied as
// DPAPI optional entropy or AEAD associated data by platform adapters.
func Binding(protection Protection, metadata Metadata) ([]byte, error) {
	if !protection.valid() || metadata.Validate() != nil {
		return nil, ErrInvalidMetadata
	}
	binding := make([]byte, 0, 20+len(metadata.KeyID)+len(metadata.Purpose))
	binding = append(binding, "beresta-keystore-v1"...)
	binding = append(binding, byte(protection))
	binding = binary.BigEndian.AppendUint16(binding, uint16(len(metadata.KeyID)))
	binding = append(binding, metadata.KeyID...)
	binding = binary.BigEndian.AppendUint16(binding, uint16(len(metadata.Purpose)))
	binding = append(binding, metadata.Purpose...)
	return binding, nil
}

// WrapError adds a non-secret operation class while preserving stable errors.
func WrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("keystore: %s: %w", operation, err)
}

package crypto

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"golang.org/x/crypto/hkdf"
)

const (
	KeyIDBytes       = 16
	SessionIDBytes   = 16
	ObjectIDBytes    = 16
	AccountIDBytes   = 16
	WorkspaceIDBytes = 16
	BackupIDBytes    = 16
	BlobIDBytes      = 32
	TranscriptBytes  = 32

	MaxObjectTypeBytes = 64
	HKDFKeyBytes       = 32
	SchemaVersionV1    = 1
)

type ObjectType string

const (
	ObjectTypeOperationPayload  ObjectType = "operation-payload"
	ObjectTypeNoteSnapshot      ObjectType = "note-snapshot"
	ObjectTypeWorkspaceSnapshot ObjectType = "workspace-snapshot"
	ObjectTypeRevision          ObjectType = "revision"
)

type HKDFDomain string

const (
	HKDFDomainKeybag          HKDFDomain = "keybag"
	HKDFDomainWorkspaceObject HKDFDomain = "workspace-object"
	HKDFDomainBlobID          HKDFDomain = "blob-id"
	HKDFDomainBlobManifest    HKDFDomain = "blob-manifest"
	HKDFDomainBlobChunk       HKDFDomain = "blob-chunk"
	HKDFDomainBackup          HKDFDomain = "backup"
	HKDFDomainPairingExport   HKDFDomain = "pairing-export"
)

var (
	ErrUnsupportedCryptoProfile = errors.New("crypto: unsupported crypto profile")
	ErrInvalidDerivationContext = errors.New("crypto: invalid derivation context")
	ErrInvalidDerivationKey     = errors.New("crypto: invalid derivation key")
)

// DeriveKeybagKey derives the key used to encrypt an account keybag.
func DeriveKeybagKey(profile string, rootKey *Secret, accountID []byte) (*Secret, error) {
	if err := requireBytes("account ID", accountID, AccountIDBytes); err != nil {
		return nil, err
	}
	return deriveHKDF(profile, rootKey, HKDFDomainKeybag, accountID)
}

// DeriveWorkspaceObjectKey derives a key bound to one object, type, and schema
// version. Profile v1 accepts only schema version 1.
func DeriveWorkspaceObjectKey(profile string, workspaceKey *Secret, objectID []byte, objectType ObjectType, schemaVersion uint32) (*Secret, error) {
	if err := requireBytes("object ID", objectID, ObjectIDBytes); err != nil {
		return nil, err
	}
	if len(objectType) > MaxObjectTypeBytes || !utf8.ValidString(string(objectType)) || !validObjectType(objectType) {
		return nil, fmt.Errorf("%w: invalid object type", ErrInvalidDerivationContext)
	}
	if schemaVersion != SchemaVersionV1 {
		return nil, ErrUnsupportedCryptoProfile
	}
	var encodedVersion [4]byte
	binary.BigEndian.PutUint32(encodedVersion[:], schemaVersion)
	return deriveHKDF(profile, workspaceKey, HKDFDomainWorkspaceObject, objectID, []byte(objectType), encodedVersion[:])
}

// DeriveBlobIDKey derives the workspace-private attachment identity key.
func DeriveBlobIDKey(profile string, workspaceKey *Secret, workspaceID []byte) (*Secret, error) {
	if err := requireBytes("workspace ID", workspaceID, WorkspaceIDBytes); err != nil {
		return nil, err
	}
	return deriveHKDF(profile, workspaceKey, HKDFDomainBlobID, workspaceID)
}

// DeriveBlobManifestKey derives the key for one encrypted attachment manifest.
func DeriveBlobManifestKey(profile string, workspaceKey *Secret, blobID []byte) (*Secret, error) {
	if err := requireBytes("blob ID", blobID, BlobIDBytes); err != nil {
		return nil, err
	}
	return deriveHKDF(profile, workspaceKey, HKDFDomainBlobManifest, blobID)
}

// DeriveBlobChunkKey derives the independent key for one attachment chunk.
func DeriveBlobChunkKey(profile string, workspaceKey *Secret, blobID []byte, chunkIndex uint64) (*Secret, error) {
	if err := requireBytes("blob ID", blobID, BlobIDBytes); err != nil {
		return nil, err
	}
	var encodedIndex [8]byte
	binary.BigEndian.PutUint64(encodedIndex[:], chunkIndex)
	return deriveHKDF(profile, workspaceKey, HKDFDomainBlobChunk, blobID, encodedIndex[:])
}

// DeriveBackupKey derives the key for one account backup.
func DeriveBackupKey(profile string, rootKey *Secret, accountID, backupID []byte) (*Secret, error) {
	if err := requireBytes("account ID", accountID, AccountIDBytes); err != nil {
		return nil, err
	}
	if err := requireBytes("backup ID", backupID, BackupIDBytes); err != nil {
		return nil, err
	}
	return deriveHKDF(profile, rootKey, HKDFDomainBackup, accountID, backupID)
}

// DerivePairingExportKey derives the key that authenticates a pairing export.
func DerivePairingExportKey(profile string, sessionKey *Secret, sessionID, transcriptHash []byte) (*Secret, error) {
	if err := requireBytes("session ID", sessionID, SessionIDBytes); err != nil {
		return nil, err
	}
	if err := requireBytes("transcript hash", transcriptHash, TranscriptBytes); err != nil {
		return nil, err
	}
	return deriveHKDF(profile, sessionKey, HKDFDomainPairingExport, sessionID, transcriptHash)
}

func deriveHKDF(profile string, inputKey *Secret, domain HKDFDomain, parts ...[]byte) (*Secret, error) {
	if profile != CryptoProfileV1 {
		return nil, ErrUnsupportedCryptoProfile
	}
	if !validHKDFDomain(domain) {
		return nil, fmt.Errorf("%w: unknown HKDF domain", ErrInvalidDerivationContext)
	}
	if inputKey == nil {
		return nil, ErrSecretClosed
	}

	info := buildHKDFInfo(profile, domain, parts...)
	derived := make([]byte, HKDFKeyBytes)
	err := inputKey.Use(func(input []byte) error {
		if len(input) != HKDFKeyBytes {
			return ErrInvalidDerivationKey
		}
		reader := hkdf.New(sha256.New, input, nil, info)
		if _, err := io.ReadFull(reader, derived); err != nil {
			return fmt.Errorf("derive HKDF output: %w", err)
		}
		return nil
	})
	if err != nil {
		wipe(derived)
		return nil, err
	}

	result, err := TakeSecret(derived)
	if err != nil {
		wipe(derived)
		return nil, err
	}
	return result, nil
}

func buildHKDFInfo(profile string, domain HKDFDomain, parts ...[]byte) []byte {
	total := 4 + len(profile) + 4 + len(domain)
	for _, part := range parts {
		total += 4 + len(part)
	}
	info := make([]byte, 0, total)
	info = appendLengthPrefixed(info, []byte(profile))
	info = appendLengthPrefixed(info, []byte(domain))
	for _, part := range parts {
		info = appendLengthPrefixed(info, part)
	}
	return info
}

func appendLengthPrefixed(destination, value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	destination = append(destination, length[:]...)
	return append(destination, value...)
}

func validHKDFDomain(domain HKDFDomain) bool {
	switch domain {
	case HKDFDomainKeybag,
		HKDFDomainWorkspaceObject,
		HKDFDomainBlobID,
		HKDFDomainBlobManifest,
		HKDFDomainBlobChunk,
		HKDFDomainBackup,
		HKDFDomainPairingExport:
		return true
	default:
		return false
	}
}

func validObjectType(objectType ObjectType) bool {
	switch objectType {
	case ObjectTypeOperationPayload,
		ObjectTypeNoteSnapshot,
		ObjectTypeWorkspaceSnapshot,
		ObjectTypeRevision:
		return true
	default:
		return false
	}
}

func requireBytes(name string, value []byte, required int) error {
	if len(value) != required {
		return fmt.Errorf("%w: %s must be %d bytes", ErrInvalidDerivationContext, name, required)
	}
	return nil
}

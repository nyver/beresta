package crypto

import "encoding/binary"

// CanonicalKeybagAAD encodes the closed keybag AAD map using RFC 8949
// deterministic ordering and shortest-width integers.
func CanonicalKeybagAAD(header KeybagHeader) ([]byte, error) {
	if err := header.validate(); err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, 256)
	encoded = appendCBORHeader(encoded, 5, 6)
	encoded = appendCBORText(encoded, "kdf")
	encoded = appendKDFHeaderCBOR(encoded, header.KDF)
	encoded = appendCBORText(encoded, "container")
	encoded = appendCBORText(encoded, "keybag")
	encoded = appendCBORText(encoded, "account_id")
	encoded = appendCBORBytes(encoded, header.AccountID)
	encoded = appendCBORText(encoded, "crypto_profile")
	encoded = appendCBORText(encoded, header.CryptoProfile)
	encoded = appendCBORText(encoded, "format_version")
	encoded = appendCBORUint(encoded, uint64(header.FormatVersion))
	encoded = appendCBORText(encoded, "keybag_version")
	encoded = appendCBORUint(encoded, header.KeybagVersion)
	return encoded, nil
}

// CanonicalObjectAAD encodes the closed encrypted-object AAD map.
func CanonicalObjectAAD(metadata ObjectMetadata) ([]byte, error) {
	if err := metadata.validate(); err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, 192)
	encoded = appendCBORHeader(encoded, 5, 6)
	encoded = appendCBORText(encoded, "key_id")
	encoded = appendCBORBytes(encoded, metadata.KeyID)
	encoded = appendCBORText(encoded, "object_id")
	encoded = appendCBORBytes(encoded, metadata.ObjectID)
	encoded = appendCBORText(encoded, "object_type")
	encoded = appendCBORText(encoded, string(metadata.ObjectType))
	encoded = appendCBORText(encoded, "workspace_id")
	encoded = appendCBORBytes(encoded, metadata.WorkspaceID)
	encoded = appendCBORText(encoded, "crypto_profile")
	encoded = appendCBORText(encoded, metadata.CryptoProfile)
	encoded = appendCBORText(encoded, "schema_version")
	encoded = appendCBORUint(encoded, uint64(metadata.SchemaVersion))
	return encoded, nil
}

// CanonicalAttachmentManifestAAD binds an encrypted manifest to its visible
// workspace, private blob identifier, and workspace-key generation.
func CanonicalAttachmentManifestAAD(metadata AttachmentMetadata) ([]byte, error) {
	if err := metadata.validate(); err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, 176)
	encoded = appendCBORHeader(encoded, 5, 5)
	encoded = appendCBORText(encoded, "key_id")
	encoded = appendCBORBytes(encoded, metadata.KeyID)
	encoded = appendCBORText(encoded, "blob_id")
	encoded = appendCBORBytes(encoded, metadata.BlobID)
	encoded = appendCBORText(encoded, "workspace_id")
	encoded = appendCBORBytes(encoded, metadata.WorkspaceID)
	encoded = appendCBORText(encoded, "crypto_profile")
	encoded = appendCBORText(encoded, metadata.CryptoProfile)
	encoded = appendCBORText(encoded, "schema_version")
	encoded = appendCBORUint(encoded, uint64(metadata.SchemaVersion))
	return encoded, nil
}

// CanonicalAttachmentChunkAAD encodes the exact chunk binding from the v1
// cryptographic profile.
func CanonicalAttachmentChunkAAD(metadata AttachmentChunkMetadata) ([]byte, error) {
	if err := metadata.validate(); err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, 192)
	encoded = appendCBORHeader(encoded, 5, 6)
	encoded = appendCBORText(encoded, "key_id")
	encoded = appendCBORBytes(encoded, metadata.KeyID)
	encoded = appendCBORText(encoded, "blob_id")
	encoded = appendCBORBytes(encoded, metadata.BlobID)
	encoded = appendCBORText(encoded, "chunk_index")
	encoded = appendCBORUint(encoded, uint64(metadata.ChunkIndex))
	encoded = appendCBORText(encoded, "workspace_id")
	encoded = appendCBORBytes(encoded, metadata.WorkspaceID)
	encoded = appendCBORText(encoded, "crypto_profile")
	encoded = appendCBORText(encoded, metadata.CryptoProfile)
	encoded = appendCBORText(encoded, "plaintext_size")
	encoded = appendCBORUint(encoded, uint64(metadata.PlaintextSize))
	return encoded, nil
}

// CanonicalBackupAAD encodes the complete immutable standalone backup header.
func CanonicalBackupAAD(header BackupHeader) ([]byte, error) {
	if err := header.validate(); err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, 320)
	encoded = appendCBORHeader(encoded, 5, 9)
	encoded = appendCBORText(encoded, "kdf")
	encoded = appendKDFHeaderCBOR(encoded, header.KDF)
	encoded = appendCBORText(encoded, "magic")
	encoded = appendCBORBytes(encoded, header.Magic)
	encoded = appendCBORText(encoded, "nonce")
	encoded = appendCBORBytes(encoded, header.Nonce)
	encoded = appendCBORText(encoded, "backup_id")
	encoded = appendCBORBytes(encoded, header.BackupID)
	encoded = appendCBORText(encoded, "account_id")
	encoded = appendCBORBytes(encoded, header.AccountID)
	encoded = appendCBORText(encoded, "crypto_profile")
	encoded = appendCBORText(encoded, header.CryptoProfile)
	encoded = appendCBORText(encoded, "format_version")
	encoded = appendCBORUint(encoded, uint64(header.FormatVersion))
	encoded = appendCBORText(encoded, "ciphertext_size")
	encoded = appendCBORUint(encoded, header.CiphertextSize)
	encoded = appendCBORText(encoded, "created_unix_ms")
	encoded = appendCBORUint(encoded, header.CreatedUnixMS)
	return encoded, nil
}

func appendKDFHeaderCBOR(destination []byte, params Argon2idParams) []byte {
	destination = appendCBORHeader(destination, 5, 6)
	destination = appendCBORText(destination, "salt")
	destination = appendCBORBytes(destination, params.Salt)
	destination = appendCBORText(destination, "algorithm")
	destination = appendCBORText(destination, params.Algorithm)
	destination = appendCBORText(destination, "time_cost")
	destination = appendCBORUint(destination, uint64(params.TimeCost))
	destination = appendCBORText(destination, "memory_kib")
	destination = appendCBORUint(destination, uint64(params.MemoryKiB))
	destination = appendCBORText(destination, "parallelism")
	destination = appendCBORUint(destination, uint64(params.Parallelism))
	destination = appendCBORText(destination, "derived_key_bytes")
	destination = appendCBORUint(destination, uint64(params.DerivedKeyBytes))
	return destination
}

func appendCBORText(destination []byte, value string) []byte {
	destination = appendCBORHeader(destination, 3, uint64(len(value)))
	return append(destination, value...)
}

func appendCBORBytes(destination, value []byte) []byte {
	destination = appendCBORHeader(destination, 2, uint64(len(value)))
	return append(destination, value...)
}

func appendCBORUint(destination []byte, value uint64) []byte {
	return appendCBORHeader(destination, 0, value)
}

func appendCBORHeader(destination []byte, major byte, value uint64) []byte {
	switch {
	case value < 24:
		return append(destination, major<<5|byte(value))
	case value <= 0xff:
		return append(destination, major<<5|24, byte(value))
	case value <= 0xffff:
		var encoded [2]byte
		binary.BigEndian.PutUint16(encoded[:], uint16(value))
		return append(destination, major<<5|25, encoded[0], encoded[1])
	case value <= 0xffffffff:
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], uint32(value))
		destination = append(destination, major<<5|26)
		return append(destination, encoded[:]...)
	default:
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], value)
		destination = append(destination, major<<5|27)
		return append(destination, encoded[:]...)
	}
}

package crypto

import (
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	AttachmentSchemaVersion              = 1
	AttachmentManifestVersion            = 1
	AttachmentChunkBytes                 = 4 * 1024 * 1024
	MaxAttachmentChunks                  = 512
	MaxAttachmentPlaintextBytes          = uint64(AttachmentChunkBytes) * MaxAttachmentChunks
	MaxAttachmentManifestCiphertextBytes = 1024 * 1024
	MaxAttachmentManifestPlaintextBytes  = MaxAttachmentManifestCiphertextBytes - AEADTagBytes
	MaxAttachmentChunkCiphertext         = AttachmentChunkBytes + AEADTagBytes
)

var (
	ErrAttachmentResourceLimit = errors.New("crypto: attachment resource limit exceeded")
	ErrAttachmentVerification  = errors.New("crypto: attachment verification failed")
	ErrNonceReuse              = errors.New("crypto: nonce reuse detected")
	ErrDuplicateChunkIndex     = errors.New("crypto: duplicate attachment chunk index")
)

// AttachmentMetadata binds an encrypted manifest to one workspace blob and
// workspace-key generation.
type AttachmentMetadata struct {
	SchemaVersion uint32 `json:"schema_version" cbor:"schema_version"`
	CryptoProfile string `json:"crypto_profile" cbor:"crypto_profile"`
	WorkspaceID   []byte `json:"workspace_id" cbor:"workspace_id"`
	BlobID        []byte `json:"blob_id" cbor:"blob_id"`
	KeyID         []byte `json:"key_id" cbor:"key_id"`
}

type EncryptedAttachmentManifest struct {
	Metadata   AttachmentMetadata `json:"metadata" cbor:"metadata"`
	Nonce      []byte             `json:"nonce" cbor:"nonce"`
	Ciphertext []byte             `json:"ciphertext" cbor:"ciphertext"`
}

type AttachmentChunkMetadata struct {
	CryptoProfile string `json:"crypto_profile" cbor:"crypto_profile"`
	WorkspaceID   []byte `json:"workspace_id" cbor:"workspace_id"`
	BlobID        []byte `json:"blob_id" cbor:"blob_id"`
	KeyID         []byte `json:"key_id" cbor:"key_id"`
	ChunkIndex    uint32 `json:"chunk_index" cbor:"chunk_index"`
	PlaintextSize uint32 `json:"plaintext_size" cbor:"plaintext_size"`
}

// EncryptedAttachmentChunk is independently authenticated and resumable.
// CiphertextSHA256 is a transport diagnostic and never replaces AEAD checks.
type EncryptedAttachmentChunk struct {
	Metadata         AttachmentChunkMetadata `json:"metadata" cbor:"metadata"`
	Nonce            []byte                  `json:"nonce" cbor:"nonce"`
	Ciphertext       []byte                  `json:"ciphertext" cbor:"ciphertext"`
	CiphertextSHA256 []byte                  `json:"ciphertext_sha256" cbor:"ciphertext_sha256"`
}

// AttachmentChunkSealer owns the nonce/index set for one attachment encryption
// operation. Create a fresh sealer for each attachment.
type AttachmentChunkSealer struct {
	mu          sync.Mutex
	random      io.Reader
	usedNonces  map[[XChaCha20NonceBytes]byte]struct{}
	usedIndexes map[uint32]struct{}
	failed      error
}

func NewAttachmentChunkSealer() *AttachmentChunkSealer {
	return newAttachmentChunkSealer(cryptorand.Reader)
}

func newAttachmentChunkSealer(random io.Reader) *AttachmentChunkSealer {
	return &AttachmentChunkSealer{
		random:      random,
		usedNonces:  make(map[[XChaCha20NonceBytes]byte]struct{}),
		usedIndexes: make(map[uint32]struct{}),
	}
}

// ComputeBlobID streams a private HMAC identity over the complete attachment.
func ComputeBlobID(ctx context.Context, profile string, workspaceKey *Secret, workspaceID []byte, plaintext io.Reader) ([]byte, uint64, error) {
	if err := contextError(ctx); err != nil {
		return nil, 0, err
	}
	if profile != CryptoProfileV1 {
		return nil, 0, ErrUnsupportedCryptoProfile
	}
	if plaintext == nil {
		return nil, 0, ErrInvalidEncryptionMetadata
	}
	blobIDKey, err := DeriveBlobIDKey(profile, workspaceKey, workspaceID)
	if err != nil {
		return nil, 0, err
	}
	defer blobIDKey.Close()

	buffer := make([]byte, 64*1024)
	defer wipe(buffer)
	var identifier []byte
	var total uint64
	err = blobIDKey.Use(func(keyBytes []byte) error {
		digest := hmac.New(sha256.New, keyBytes)
		for {
			if err := contextError(ctx); err != nil {
				return err
			}
			read, readErr := plaintext.Read(buffer)
			if read > 0 {
				if total > MaxAttachmentPlaintextBytes-uint64(read) {
					return ErrAttachmentResourceLimit
				}
				total += uint64(read)
				if _, err := digest.Write(buffer[:read]); err != nil {
					return fmt.Errorf("compute private blob identity: %w", err)
				}
				wipe(buffer[:read])
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					identifier = digest.Sum(nil)
					return nil
				}
				return fmt.Errorf("read attachment plaintext: %w", readErr)
			}
			if read == 0 {
				return io.ErrNoProgress
			}
		}
	})
	if err != nil {
		wipe(identifier)
		return nil, 0, err
	}
	return identifier, total, nil
}

// EncryptManifest seals a bounded canonical attachment-manifest payload.
func EncryptManifest(workspaceKey *Secret, metadata AttachmentMetadata, canonicalManifest *Secret) (EncryptedAttachmentManifest, error) {
	return encryptManifest(workspaceKey, metadata, canonicalManifest, cryptorand.Reader)
}

func encryptManifest(workspaceKey *Secret, metadata AttachmentMetadata, canonicalManifest *Secret, random io.Reader) (EncryptedAttachmentManifest, error) {
	if err := metadata.validate(); err != nil {
		return EncryptedAttachmentManifest{}, err
	}
	if canonicalManifest == nil {
		return EncryptedAttachmentManifest{}, ErrSecretClosed
	}
	if canonicalManifest.Len() > MaxAttachmentManifestPlaintextBytes {
		return EncryptedAttachmentManifest{}, ErrAttachmentResourceLimit
	}
	aad, err := CanonicalAttachmentManifestAAD(metadata)
	if err != nil {
		return EncryptedAttachmentManifest{}, err
	}
	key, err := DeriveBlobManifestKey(metadata.CryptoProfile, workspaceKey, metadata.BlobID)
	if err != nil {
		return EncryptedAttachmentManifest{}, err
	}
	defer key.Close()
	nonce, ciphertext, err := sealXChaCha(key, canonicalManifest, aad, random)
	if err != nil {
		return EncryptedAttachmentManifest{}, err
	}
	return EncryptedAttachmentManifest{
		Metadata:   cloneAttachmentMetadata(metadata),
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}, nil
}

func OpenManifest(workspaceKey *Secret, envelope EncryptedAttachmentManifest) (*Secret, error) {
	if err := envelope.validate(); err != nil {
		return nil, err
	}
	aad, err := CanonicalAttachmentManifestAAD(envelope.Metadata)
	if err != nil {
		return nil, err
	}
	key, err := DeriveBlobManifestKey(envelope.Metadata.CryptoProfile, workspaceKey, envelope.Metadata.BlobID)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	return openXChaCha(key, envelope.Nonce, envelope.Ciphertext, aad)
}

// SealChunk encrypts one chunk. plaintext is caller-owned and is not mutated.
func (sealer *AttachmentChunkSealer) SealChunk(workspaceKey *Secret, metadata AttachmentMetadata, index uint32, plaintext []byte) (EncryptedAttachmentChunk, error) {
	if sealer == nil || sealer.random == nil {
		return EncryptedAttachmentChunk{}, ErrRandomSource
	}
	if err := metadata.validate(); err != nil {
		return EncryptedAttachmentChunk{}, err
	}
	if index >= MaxAttachmentChunks || len(plaintext) > AttachmentChunkBytes {
		return EncryptedAttachmentChunk{}, ErrAttachmentResourceLimit
	}
	chunkMetadata := AttachmentChunkMetadata{
		CryptoProfile: metadata.CryptoProfile,
		WorkspaceID:   append([]byte(nil), metadata.WorkspaceID...),
		BlobID:        append([]byte(nil), metadata.BlobID...),
		KeyID:         append([]byte(nil), metadata.KeyID...),
		ChunkIndex:    index,
		PlaintextSize: uint32(len(plaintext)),
	}
	aad, err := CanonicalAttachmentChunkAAD(chunkMetadata)
	if err != nil {
		return EncryptedAttachmentChunk{}, err
	}

	nonce, err := sealer.reserveNonce(index)
	if err != nil {
		return EncryptedAttachmentChunk{}, err
	}
	key, err := DeriveBlobChunkKey(metadata.CryptoProfile, workspaceKey, metadata.BlobID, uint64(index))
	if err != nil {
		return EncryptedAttachmentChunk{}, err
	}
	defer key.Close()
	ciphertext, err := sealXChaChaWithNonce(key, plaintext, nonce[:], aad)
	if err != nil {
		return EncryptedAttachmentChunk{}, err
	}
	hash := sha256.Sum256(ciphertext)
	return EncryptedAttachmentChunk{
		Metadata:         chunkMetadata,
		Nonce:            append([]byte(nil), nonce[:]...),
		Ciphertext:       ciphertext,
		CiphertextSHA256: append([]byte(nil), hash[:]...),
	}, nil
}

// OpenChunk verifies the diagnostic ciphertext hash and AEAD before returning
// caller-owned mutable plaintext. Callers wipe it after committing to encrypted
// storage or writing into a non-visible temporary verification target.
func OpenChunk(workspaceKey *Secret, chunk EncryptedAttachmentChunk) ([]byte, error) {
	if err := chunk.validate(); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(chunk.Ciphertext)
	if !hmac.Equal(hash[:], chunk.CiphertextSHA256) {
		return nil, ErrAttachmentVerification
	}
	aad, err := CanonicalAttachmentChunkAAD(chunk.Metadata)
	if err != nil {
		return nil, err
	}
	key, err := DeriveBlobChunkKey(chunk.Metadata.CryptoProfile, workspaceKey, chunk.Metadata.BlobID, uint64(chunk.Metadata.ChunkIndex))
	if err != nil {
		return nil, err
	}
	defer key.Close()
	plaintext, err := openXChaChaBytes(key, chunk.Nonce, chunk.Ciphertext, aad, MaxAttachmentChunkCiphertext)
	if err != nil {
		return nil, err
	}
	if len(plaintext) != int(chunk.Metadata.PlaintextSize) {
		wipe(plaintext)
		return nil, ErrAttachmentVerification
	}
	return plaintext, nil
}

// VerifyAttachment authenticates a complete contiguous chunk set, writes
// plaintext to a caller-owned non-visible temporary destination, and confirms
// total size plus the workspace-private blob ID before success. The caller must
// discard or wipe the destination when this function returns an error.
func VerifyAttachment(ctx context.Context, workspaceKey *Secret, metadata AttachmentMetadata, chunks []EncryptedAttachmentChunk, destination io.Writer) (uint64, error) {
	if err := contextError(ctx); err != nil {
		return 0, err
	}
	if err := metadata.validate(); err != nil {
		return 0, err
	}
	if destination == nil || len(chunks) == 0 || len(chunks) > MaxAttachmentChunks {
		return 0, ErrAttachmentVerification
	}
	blobIDKey, err := DeriveBlobIDKey(metadata.CryptoProfile, workspaceKey, metadata.WorkspaceID)
	if err != nil {
		return 0, err
	}
	defer blobIDKey.Close()

	var total uint64
	err = blobIDKey.Use(func(keyBytes []byte) error {
		digest := hmac.New(sha256.New, keyBytes)
		for index, chunk := range chunks {
			if err := contextError(ctx); err != nil {
				return err
			}
			if !chunkMatchesAttachment(chunk.Metadata, metadata, uint32(index)) {
				return ErrAttachmentVerification
			}
			if len(chunks) > 1 && (index < len(chunks)-1 && chunk.Metadata.PlaintextSize != AttachmentChunkBytes || chunk.Metadata.PlaintextSize == 0) {
				return ErrAttachmentVerification
			}
			plaintext, err := OpenChunk(workspaceKey, chunk)
			if err != nil {
				return err
			}
			if total > MaxAttachmentPlaintextBytes-uint64(len(plaintext)) {
				wipe(plaintext)
				return ErrAttachmentResourceLimit
			}
			total += uint64(len(plaintext))
			_, _ = digest.Write(plaintext)
			written, writeErr := destination.Write(plaintext)
			wipe(plaintext)
			if writeErr != nil {
				return fmt.Errorf("write verified attachment plaintext: %w", writeErr)
			}
			if written != int(chunk.Metadata.PlaintextSize) {
				return io.ErrShortWrite
			}
		}
		computed := digest.Sum(nil)
		defer wipe(computed)
		if !hmac.Equal(computed, metadata.BlobID) {
			return ErrAttachmentVerification
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func (sealer *AttachmentChunkSealer) reserveNonce(index uint32) ([XChaCha20NonceBytes]byte, error) {
	sealer.mu.Lock()
	defer sealer.mu.Unlock()
	var nonce [XChaCha20NonceBytes]byte
	if sealer.failed != nil {
		return nonce, sealer.failed
	}
	if len(sealer.usedIndexes) >= MaxAttachmentChunks {
		return nonce, ErrAttachmentResourceLimit
	}
	if _, exists := sealer.usedIndexes[index]; exists {
		return nonce, ErrDuplicateChunkIndex
	}
	if _, err := io.ReadFull(sealer.random, nonce[:]); err != nil {
		wipe(nonce[:])
		sealer.failed = fmt.Errorf("%w: attachment chunk nonce generation", ErrRandomSource)
		return nonce, sealer.failed
	}
	if _, exists := sealer.usedNonces[nonce]; exists {
		wipe(nonce[:])
		sealer.failed = ErrNonceReuse
		return nonce, sealer.failed
	}
	sealer.usedIndexes[index] = struct{}{}
	sealer.usedNonces[nonce] = struct{}{}
	return nonce, nil
}

func sealXChaChaWithNonce(key *Secret, plaintext, nonce, aad []byte) ([]byte, error) {
	if key == nil {
		return nil, ErrSecretClosed
	}
	if len(nonce) != XChaCha20NonceBytes {
		return nil, ErrInvalidEncryptionMetadata
	}
	var ciphertext []byte
	err := key.Use(func(keyBytes []byte) error {
		if len(keyBytes) != chacha20poly1305.KeySize {
			return ErrInvalidEncryptionKey
		}
		aead, err := chacha20poly1305.NewX(keyBytes)
		if err != nil {
			return ErrInvalidEncryptionKey
		}
		ciphertext = aead.Seal(nil, nonce, plaintext, aad)
		return nil
	})
	if err != nil {
		wipe(ciphertext)
		return nil, err
	}
	return ciphertext, nil
}

func openXChaChaBytes(key *Secret, nonce, ciphertext, aad []byte, maxCiphertext int) ([]byte, error) {
	if key == nil {
		return nil, ErrSecretClosed
	}
	if len(nonce) != XChaCha20NonceBytes || len(ciphertext) < AEADTagBytes || len(ciphertext) > maxCiphertext {
		return nil, ErrInvalidEncryptionMetadata
	}
	var plaintext []byte
	var openErr error
	err := key.Use(func(keyBytes []byte) error {
		if len(keyBytes) != chacha20poly1305.KeySize {
			return ErrInvalidEncryptionKey
		}
		aead, err := chacha20poly1305.NewX(keyBytes)
		if err != nil {
			return ErrInvalidEncryptionKey
		}
		plaintext, openErr = aead.Open(nil, nonce, ciphertext, aad)
		return nil
	})
	if err != nil {
		wipe(plaintext)
		return nil, err
	}
	if openErr != nil {
		wipe(plaintext)
		return nil, ErrAuthentication
	}
	return plaintext, nil
}

func (metadata AttachmentMetadata) validate() error {
	if metadata.CryptoProfile != CryptoProfileV1 || metadata.SchemaVersion != AttachmentSchemaVersion {
		return ErrUnsupportedCryptoProfile
	}
	if len(metadata.WorkspaceID) != WorkspaceIDBytes || len(metadata.BlobID) != BlobIDBytes || len(metadata.KeyID) != KeyIDBytes {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func (metadata AttachmentChunkMetadata) validate() error {
	if metadata.CryptoProfile != CryptoProfileV1 {
		return ErrUnsupportedCryptoProfile
	}
	if len(metadata.WorkspaceID) != WorkspaceIDBytes || len(metadata.BlobID) != BlobIDBytes || len(metadata.KeyID) != KeyIDBytes {
		return ErrInvalidEncryptionMetadata
	}
	if metadata.ChunkIndex >= MaxAttachmentChunks || metadata.PlaintextSize > AttachmentChunkBytes {
		return ErrAttachmentResourceLimit
	}
	return nil
}

func (envelope EncryptedAttachmentManifest) validate() error {
	if err := envelope.Metadata.validate(); err != nil {
		return err
	}
	if len(envelope.Nonce) != XChaCha20NonceBytes || len(envelope.Ciphertext) < AEADTagBytes || len(envelope.Ciphertext) > MaxAttachmentManifestCiphertextBytes {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func (chunk EncryptedAttachmentChunk) validate() error {
	if err := chunk.Metadata.validate(); err != nil {
		return err
	}
	if len(chunk.Nonce) != XChaCha20NonceBytes || len(chunk.CiphertextSHA256) != sha256.Size || len(chunk.Ciphertext) != int(chunk.Metadata.PlaintextSize)+AEADTagBytes {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func cloneAttachmentMetadata(metadata AttachmentMetadata) AttachmentMetadata {
	metadata.WorkspaceID = append([]byte(nil), metadata.WorkspaceID...)
	metadata.BlobID = append([]byte(nil), metadata.BlobID...)
	metadata.KeyID = append([]byte(nil), metadata.KeyID...)
	return metadata
}

func chunkMatchesAttachment(chunk AttachmentChunkMetadata, attachment AttachmentMetadata, expectedIndex uint32) bool {
	return chunk.CryptoProfile == attachment.CryptoProfile &&
		chunk.ChunkIndex == expectedIndex &&
		bytesEqual(chunk.WorkspaceID, attachment.WorkspaceID) &&
		bytesEqual(chunk.BlobID, attachment.BlobID) &&
		bytesEqual(chunk.KeyID, attachment.KeyID)
}

func bytesEqual(left, right []byte) bool {
	return hmac.Equal(left, right)
}

package crypto

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateBlobIDStreamsAndHidesCrossWorkspaceEquality(t *testing.T) {
	workspaceKeyBytes := bytes.Repeat([]byte{0x51}, HKDFKeyBytes)
	workspaceKey := takeTestSecret(t, workspaceKeyBytes)
	defer workspaceKey.Close()
	workspaceA := bytes.Repeat([]byte{0x11}, WorkspaceIDBytes)
	workspaceB := bytes.Repeat([]byte{0x12}, WorkspaceIDBytes)
	plaintext := bytes.Repeat([]byte("beresta-attachment"), 10000)

	idA, size, err := ComputeBlobID(context.Background(), CryptoProfileV1, workspaceKey, workspaceA, bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	if size != uint64(len(plaintext)) {
		t.Fatalf("plaintext size = %d, want %d", size, len(plaintext))
	}
	idARepeat, _, err := ComputeBlobID(context.Background(), CryptoProfileV1, workspaceKey, workspaceA, bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(idA, idARepeat) {
		t.Fatal("same workspace and plaintext produced different private IDs")
	}
	idB, _, err := ComputeBlobID(context.Background(), CryptoProfileV1, workspaceKey, workspaceB, bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(idA, idB) {
		t.Fatal("equal plaintext leaked equality across workspace domains")
	}

	blobIDKey := mustDeriveBlobIDKey(t, workspaceKeyBytes, workspaceA)
	reference := hmac.New(sha256.New, blobIDKey)
	_, _ = reference.Write(plaintext)
	wipe(blobIDKey)
	if !hmac.Equal(idA, reference.Sum(nil)) {
		t.Fatalf("private blob ID = %x, want %x", idA, reference.Sum(nil))
	}
}

func TestPrivateBlobIDHandlesEmptyCancellationAndReadFailure(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x21}, HKDFKeyBytes))
	defer workspaceKey.Close()
	workspaceID := bytes.Repeat([]byte{0x22}, WorkspaceIDBytes)
	id, size, err := ComputeBlobID(context.Background(), CryptoProfileV1, workspaceKey, workspaceID, bytes.NewReader(nil))
	if err != nil || size != 0 || len(id) != BlobIDBytes {
		t.Fatalf("empty ID = %x, size = %d, error = %v", id, size, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if id, size, err = ComputeBlobID(cancelled, CryptoProfileV1, workspaceKey, workspaceID, bytes.NewReader(nil)); !errors.Is(err, context.Canceled) || id != nil || size != 0 {
		t.Fatalf("cancelled ID = %x, size = %d, error = %v", id, size, err)
	}
	reader := io.MultiReader(bytes.NewReader([]byte("partial")), failingIdentityReader{})
	if id, size, err = ComputeBlobID(context.Background(), CryptoProfileV1, workspaceKey, workspaceID, reader); err == nil || id != nil || size != 0 {
		t.Fatalf("failed ID = %x, size = %d, error = %v", id, size, err)
	}
	if id, size, err = ComputeBlobID(context.Background(), "beresta.crypto.v2", workspaceKey, workspaceID, bytes.NewReader(nil)); !errors.Is(err, ErrUnsupportedCryptoProfile) || id != nil || size != 0 {
		t.Fatalf("future-profile ID = %x, size = %d, error = %v", id, size, err)
	}
	if workspaceKey.Len() != HKDFKeyBytes {
		t.Fatal("future profile validation used or wiped the workspace key")
	}
}

func TestAttachmentManifestRoundTripAndBinding(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x31}, HKDFKeyBytes))
	defer workspaceKey.Close()
	metadata := testAttachmentMetadata()
	manifestBytes := []byte{0xa2, 0x6b, 0x63, 0x68, 0x75, 0x6e, 0x6b, 0x5f, 0x63, 0x6f, 0x75, 0x6e, 0x74, 0x01, 0x70, 0x6d, 0x61, 0x6e, 0x69, 0x66, 0x65, 0x73, 0x74, 0x5f, 0x76, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e, 0x01}
	manifest := takeTestSecret(t, manifestBytes)
	defer manifest.Close()
	envelope, err := encryptManifest(workspaceKey, metadata, manifest, bytes.NewReader(sequentialBytes(1, XChaCha20NonceBytes)))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenManifest(workspaceKey, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if got := copySecret(t, opened); !bytes.Equal(got, manifestBytes) {
		t.Fatalf("manifest = %x, want %x", got, manifestBytes)
	}
	opened.Close()

	for _, mutate := range []func(*EncryptedAttachmentManifest){
		func(candidate *EncryptedAttachmentManifest) { candidate.Metadata.WorkspaceID[0] ^= 1 },
		func(candidate *EncryptedAttachmentManifest) { candidate.Metadata.BlobID[0] ^= 1 },
		func(candidate *EncryptedAttachmentManifest) { candidate.Metadata.KeyID[0] ^= 1 },
		func(candidate *EncryptedAttachmentManifest) { candidate.Ciphertext[0] ^= 1 },
	} {
		candidate := cloneEncryptedManifest(envelope)
		mutate(&candidate)
		if opened, err := OpenManifest(workspaceKey, candidate); !errors.Is(err, ErrAuthentication) || opened != nil {
			t.Fatalf("substituted manifest result = %v, error = %v", opened, err)
		}
	}
}

func TestAttachmentChunksRoundTripBoundariesAndTamperRejection(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x41}, HKDFKeyBytes))
	defer workspaceKey.Close()
	metadata := testAttachmentMetadata()
	random := bytes.NewReader(sequentialBytes(1, XChaCha20NonceBytes*3))
	sealer := newAttachmentChunkSealer(random)
	chunks := [][]byte{
		{},
		[]byte("middle chunk"),
		bytes.Repeat([]byte{0x7a}, AttachmentChunkBytes),
	}
	var encrypted []EncryptedAttachmentChunk
	for index, plaintext := range chunks {
		chunk, err := sealer.SealChunk(workspaceKey, metadata, uint32(index), plaintext)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := OpenChunk(workspaceKey, chunk)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(opened, plaintext) {
			t.Fatalf("chunk %d plaintext mismatch", index)
		}
		wipe(opened)
		encrypted = append(encrypted, chunk)
	}

	tampered := cloneEncryptedChunk(encrypted[1])
	tampered.Ciphertext[0] ^= 1
	if opened, err := OpenChunk(workspaceKey, tampered); !errors.Is(err, ErrAttachmentVerification) || opened != nil {
		t.Fatalf("diagnostic hash result = %x, error = %v", opened, err)
	}
	hash := sha256.Sum256(tampered.Ciphertext)
	tampered.CiphertextSHA256 = append([]byte(nil), hash[:]...)
	if opened, err := OpenChunk(workspaceKey, tampered); !errors.Is(err, ErrAuthentication) || opened != nil {
		t.Fatalf("authenticated tamper result = %x, error = %v", opened, err)
	}
	crossWorkspace := cloneEncryptedChunk(encrypted[1])
	crossWorkspace.Metadata.WorkspaceID[0] ^= 1
	if opened, err := OpenChunk(workspaceKey, crossWorkspace); !errors.Is(err, ErrAuthentication) || opened != nil {
		t.Fatalf("cross-workspace result = %x, error = %v", opened, err)
	}
}

func TestAttachmentChunkSealerRejectsDuplicateNonceAndIndex(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x61}, HKDFKeyBytes))
	defer workspaceKey.Close()
	metadata := testAttachmentMetadata()
	repeatedNonce := bytes.Repeat([]byte{0x77}, XChaCha20NonceBytes*2)
	sealer := newAttachmentChunkSealer(bytes.NewReader(repeatedNonce))
	if _, err := sealer.SealChunk(workspaceKey, metadata, 0, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if chunk, err := sealer.SealChunk(workspaceKey, metadata, 1, []byte("second")); !errors.Is(err, ErrNonceReuse) || chunk.Ciphertext != nil {
		t.Fatalf("reused nonce chunk = %+v, error = %v", chunk, err)
	}
	if chunk, err := sealer.SealChunk(workspaceKey, metadata, 2, []byte("must stay failed")); !errors.Is(err, ErrNonceReuse) || chunk.Ciphertext != nil {
		t.Fatalf("poisoned sealer chunk = %+v, error = %v", chunk, err)
	}

	indexSealer := newAttachmentChunkSealer(bytes.NewReader(sequentialBytes(1, XChaCha20NonceBytes*2)))
	if _, err := indexSealer.SealChunk(workspaceKey, metadata, 5, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if chunk, err := indexSealer.SealChunk(workspaceKey, metadata, 5, []byte("duplicate")); !errors.Is(err, ErrDuplicateChunkIndex) || chunk.Ciphertext != nil {
		t.Fatalf("duplicate index chunk = %+v, error = %v", chunk, err)
	}
}

func TestVerifyAttachmentRequiresCompleteCanonicalChunkSetAndPrivateID(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x71}, HKDFKeyBytes))
	defer workspaceKey.Close()
	workspaceID := bytes.Repeat([]byte{0x72}, WorkspaceIDBytes)
	plaintext := append(bytes.Repeat([]byte{0x73}, AttachmentChunkBytes), []byte("final")...)
	blobID, _, err := ComputeBlobID(context.Background(), CryptoProfileV1, workspaceKey, workspaceID, bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	metadata := AttachmentMetadata{
		SchemaVersion: AttachmentSchemaVersion,
		CryptoProfile: CryptoProfileV1,
		WorkspaceID:   workspaceID,
		BlobID:        blobID,
		KeyID:         bytes.Repeat([]byte{0x74}, KeyIDBytes),
	}
	sealer := newAttachmentChunkSealer(bytes.NewReader(sequentialBytes(1, XChaCha20NonceBytes*2)))
	first, err := sealer.SealChunk(workspaceKey, metadata, 0, plaintext[:AttachmentChunkBytes])
	if err != nil {
		t.Fatal(err)
	}
	last, err := sealer.SealChunk(workspaceKey, metadata, 1, plaintext[AttachmentChunkBytes:])
	if err != nil {
		t.Fatal(err)
	}
	var verified bytes.Buffer
	total, err := VerifyAttachment(context.Background(), workspaceKey, metadata, []EncryptedAttachmentChunk{first, last}, &verified)
	if err != nil {
		t.Fatal(err)
	}
	if total != uint64(len(plaintext)) || !bytes.Equal(verified.Bytes(), plaintext) {
		t.Fatalf("verified size/content mismatch: %d bytes", total)
	}

	wrongID := cloneAttachmentMetadata(metadata)
	wrongID.BlobID[0] ^= 1
	if total, err = VerifyAttachment(context.Background(), workspaceKey, wrongID, []EncryptedAttachmentChunk{first, last}, io.Discard); !errors.Is(err, ErrAttachmentVerification) || total != 0 {
		t.Fatalf("wrong private ID size = %d, error = %v", total, err)
	}
	if total, err = VerifyAttachment(context.Background(), workspaceKey, metadata, []EncryptedAttachmentChunk{last, first}, io.Discard); !errors.Is(err, ErrAttachmentVerification) || total != 0 {
		t.Fatalf("reordered chunks size = %d, error = %v", total, err)
	}
}

func TestAttachmentCompatibilityVectorValues(t *testing.T) {
	var vector attachmentCompatibilityVector
	fixturePath := filepath.Join("..", "..", "schema", "testdata", "v1", "crypto-attachment.json")
	encoded, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &vector); err != nil {
		t.Fatal(err)
	}
	if vector.FixtureFormat != 1 || vector.CryptoProfile != CryptoProfileV1 {
		t.Fatalf("unsupported attachment fixture header: format=%d profile=%q", vector.FixtureFormat, vector.CryptoProfile)
	}
	workspaceKey := takeTestSecret(t, decodeVectorHex(t, vector.WorkspaceKeyHex))
	defer workspaceKey.Close()
	workspaceID := decodeVectorHex(t, vector.WorkspaceIDHex)
	chunkPlaintext := decodeVectorHex(t, vector.ChunkPlaintextHex)
	blobID, _, err := ComputeBlobID(context.Background(), vector.CryptoProfile, workspaceKey, workspaceID, bytes.NewReader(chunkPlaintext))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(blobID, decodeVectorHex(t, vector.BlobIDHex)) {
		t.Fatalf("private blob ID = %x", blobID)
	}
	metadata := AttachmentMetadata{SchemaVersion: AttachmentSchemaVersion, CryptoProfile: vector.CryptoProfile, WorkspaceID: workspaceID, BlobID: blobID, KeyID: decodeVectorHex(t, vector.KeyIDHex)}
	manifestBytes := decodeVectorHex(t, vector.Manifest.PlaintextHex)
	manifest := takeTestSecret(t, manifestBytes)
	defer manifest.Close()
	encryptedManifest, err := encryptManifest(workspaceKey, metadata, manifest, bytes.NewReader(decodeVectorHex(t, vector.Manifest.NonceHex)))
	if err != nil {
		t.Fatal(err)
	}
	sealer := newAttachmentChunkSealer(bytes.NewReader(decodeVectorHex(t, vector.Chunk.NonceHex)))
	chunk, err := sealer.SealChunk(workspaceKey, metadata, vector.Chunk.Index, chunkPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	manifestAAD, _ := CanonicalAttachmentManifestAAD(metadata)
	chunkAAD, _ := CanonicalAttachmentChunkAAD(chunk.Metadata)
	if !bytes.Equal(manifestAAD, decodeVectorHex(t, vector.Manifest.AADHex)) || !bytes.Equal(encryptedManifest.Nonce, decodeVectorHex(t, vector.Manifest.NonceHex)) || !bytes.Equal(encryptedManifest.Ciphertext, decodeVectorHex(t, vector.Manifest.CiphertextHex)) {
		t.Fatalf("manifest vector mismatch: AAD=%x nonce=%x ciphertext=%x", manifestAAD, encryptedManifest.Nonce, encryptedManifest.Ciphertext)
	}
	if !bytes.Equal(chunkAAD, decodeVectorHex(t, vector.Chunk.AADHex)) || !bytes.Equal(chunk.Nonce, decodeVectorHex(t, vector.Chunk.NonceHex)) || !bytes.Equal(chunk.Ciphertext, decodeVectorHex(t, vector.Chunk.CiphertextHex)) || !bytes.Equal(chunk.CiphertextSHA256, decodeVectorHex(t, vector.Chunk.CiphertextSHA256Hex)) {
		t.Fatalf("chunk vector mismatch: AAD=%x nonce=%x ciphertext=%x hash=%x", chunkAAD, chunk.Nonce, chunk.Ciphertext, chunk.CiphertextSHA256)
	}
}

type attachmentCompatibilityVector struct {
	FixtureFormat     int    `json:"fixture_format"`
	CryptoProfile     string `json:"crypto_profile"`
	WorkspaceKeyHex   string `json:"workspace_key_hex"`
	WorkspaceIDHex    string `json:"workspace_id_hex"`
	KeyIDHex          string `json:"key_id_hex"`
	ChunkPlaintextHex string `json:"chunk_plaintext_hex"`
	BlobIDHex         string `json:"blob_id_hex"`
	Manifest          struct {
		PlaintextHex  string `json:"plaintext_hex"`
		AADHex        string `json:"aad_hex"`
		NonceHex      string `json:"nonce_hex"`
		CiphertextHex string `json:"ciphertext_hex"`
	} `json:"manifest"`
	Chunk struct {
		Index               uint32 `json:"index"`
		AADHex              string `json:"aad_hex"`
		NonceHex            string `json:"nonce_hex"`
		CiphertextHex       string `json:"ciphertext_hex"`
		CiphertextSHA256Hex string `json:"ciphertext_sha256_hex"`
	} `json:"chunk"`
}

func mustDeriveBlobIDKey(t *testing.T, workspaceKeyBytes, workspaceID []byte) []byte {
	t.Helper()
	workspaceKey := takeTestSecret(t, workspaceKeyBytes)
	defer workspaceKey.Close()
	key, err := DeriveBlobIDKey(CryptoProfileV1, workspaceKey, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	return copySecret(t, key)
}

func testAttachmentMetadata() AttachmentMetadata {
	return AttachmentMetadata{
		SchemaVersion: AttachmentSchemaVersion,
		CryptoProfile: CryptoProfileV1,
		WorkspaceID:   bytes.Repeat([]byte{0x22}, WorkspaceIDBytes),
		BlobID:        bytes.Repeat([]byte{0x33}, BlobIDBytes),
		KeyID:         bytes.Repeat([]byte{0x44}, KeyIDBytes),
	}
}

func cloneEncryptedManifest(envelope EncryptedAttachmentManifest) EncryptedAttachmentManifest {
	envelope.Metadata = cloneAttachmentMetadata(envelope.Metadata)
	envelope.Nonce = append([]byte(nil), envelope.Nonce...)
	envelope.Ciphertext = append([]byte(nil), envelope.Ciphertext...)
	return envelope
}

func cloneEncryptedChunk(chunk EncryptedAttachmentChunk) EncryptedAttachmentChunk {
	chunk.Metadata.WorkspaceID = append([]byte(nil), chunk.Metadata.WorkspaceID...)
	chunk.Metadata.BlobID = append([]byte(nil), chunk.Metadata.BlobID...)
	chunk.Metadata.KeyID = append([]byte(nil), chunk.Metadata.KeyID...)
	chunk.Nonce = append([]byte(nil), chunk.Nonce...)
	chunk.Ciphertext = append([]byte(nil), chunk.Ciphertext...)
	chunk.CiphertextSHA256 = append([]byte(nil), chunk.CiphertextSHA256...)
	return chunk
}

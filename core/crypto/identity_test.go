package crypto

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/nacl/box"
)

func TestX25519AnonymousEnvelopeMatchesReference(t *testing.T) {
	recipientRandom := sequentialBytes(1, X25519PrivateKeyBytes)
	publicKey, privateKey, err := generateX25519Identity(bytes.NewReader(recipientRandom))
	if err != nil {
		t.Fatal(err)
	}
	defer privateKey.Close()

	plaintextBytes := []byte("canonical workspace-key envelope fixture")
	plaintext := takeTestSecret(t, plaintextBytes)
	defer plaintext.Close()
	sealRandom := sequentialBytes(101, X25519PrivateKeyBytes)
	sealed, err := sealWorkspaceKeyEnvelope(CryptoProfileV1, publicKey, plaintext, bytes.NewReader(sealRandom))
	if err != nil {
		t.Fatal(err)
	}

	var recipient [X25519PublicKeyBytes]byte
	copy(recipient[:], publicKey)
	want, err := box.SealAnonymous(nil, plaintextBytes, &recipient, bytes.NewReader(sealRandom))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sealed, want) {
		t.Fatalf("sealed envelope = %x, want %x", sealed, want)
	}

	opened, err := OpenWorkspaceKeyEnvelope(CryptoProfileV1, publicKey, privateKey, sealed)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if got := copySecret(t, opened); !bytes.Equal(got, plaintextBytes) {
		t.Fatalf("opened plaintext = %x, want %x", got, plaintextBytes)
	}
	if !bytes.Equal(copySecret(t, plaintext), plaintextBytes) {
		t.Fatal("sealing mutated the caller-owned plaintext")
	}
}

func TestX25519EnvelopeRejectsTamperingWithoutDestroyingIdentity(t *testing.T) {
	publicKey, privateKey, err := generateX25519Identity(bytes.NewReader(sequentialBytes(7, X25519PrivateKeyBytes)))
	if err != nil {
		t.Fatal(err)
	}
	defer privateKey.Close()
	privateBefore := copySecret(t, privateKey)
	plaintext := takeTestSecret(t, []byte("workspace key material"))
	defer plaintext.Close()
	sealed, err := sealWorkspaceKeyEnvelope(CryptoProfileV1, publicKey, plaintext, bytes.NewReader(sequentialBytes(77, X25519PrivateKeyBytes)))
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 0x80

	opened, err := OpenWorkspaceKeyEnvelope(CryptoProfileV1, publicKey, privateKey, sealed)
	if !errors.Is(err, ErrEnvelopeAuthentication) || opened != nil {
		t.Fatalf("tampered envelope result = %v, error = %v", opened, err)
	}
	if got := copySecret(t, privateKey); !bytes.Equal(got, privateBefore) {
		t.Fatal("attacker-controlled authentication failure mutated the identity key")
	}

	otherPublic, otherPrivate, err := generateX25519Identity(bytes.NewReader(sequentialBytes(33, X25519PrivateKeyBytes)))
	if err != nil {
		t.Fatal(err)
	}
	defer otherPrivate.Close()
	if opened, err = OpenWorkspaceKeyEnvelope(CryptoProfileV1, otherPublic, otherPrivate, sealed); !errors.Is(err, ErrEnvelopeAuthentication) || opened != nil {
		t.Fatalf("wrong-recipient result = %v, error = %v", opened, err)
	}
}

func TestX25519EnvelopeValidationAndRandomFailures(t *testing.T) {
	publicKey, privateKey, err := generateX25519Identity(bytes.NewReader(sequentialBytes(1, X25519PrivateKeyBytes)))
	if err != nil {
		t.Fatal(err)
	}
	defer privateKey.Close()
	plaintext := takeTestSecret(t, []byte("payload"))
	defer plaintext.Close()

	if sealed, err := SealWorkspaceKeyEnvelope("beresta.crypto.v2", publicKey, plaintext); !errors.Is(err, ErrUnsupportedCryptoProfile) || sealed != nil {
		t.Fatalf("future profile result = %x, error = %v", sealed, err)
	}
	if sealed, err := SealWorkspaceKeyEnvelope(CryptoProfileV1, publicKey[:31], plaintext); !errors.Is(err, ErrInvalidPublicKey) || sealed != nil {
		t.Fatalf("short public key result = %x, error = %v", sealed, err)
	}
	if sealed, err := sealWorkspaceKeyEnvelope(CryptoProfileV1, publicKey, plaintext, failingIdentityReader{}); !errors.Is(err, ErrRandomSource) || sealed != nil {
		t.Fatalf("random failure result = %x, error = %v", sealed, err)
	}
	if opened, err := OpenWorkspaceKeyEnvelope(CryptoProfileV1, publicKey, privateKey, make([]byte, box.AnonymousOverhead)); !errors.Is(err, ErrMalformedEnvelope) || opened != nil {
		t.Fatalf("short envelope result = %v, error = %v", opened, err)
	}

	wrongSizePrivate := takeTestSecret(t, make([]byte, X25519PrivateKeyBytes-1))
	sealed := make([]byte, box.AnonymousOverhead+1)
	if opened, err := OpenWorkspaceKeyEnvelope(CryptoProfileV1, publicKey, wrongSizePrivate, sealed); !errors.Is(err, ErrInvalidPrivateKey) || opened != nil {
		t.Fatalf("wrong private key result = %v, error = %v", opened, err)
	}
	if wrongSizePrivate.Len() != 0 {
		t.Fatal("invalid private key was not wiped")
	}
}

func TestEd25519DomainSeparatedSignatures(t *testing.T) {
	publicKey, privateKey, err := generateEd25519Key(bytes.NewReader(sequentialBytes(19, ed25519.SeedSize)))
	if err != nil {
		t.Fatal(err)
	}
	defer privateKey.Close()
	payload := []byte{0xa2, 0x61, 0x61, 0x01, 0x61, 0x62, 0x02}

	signature, err := SignCanonical(CryptoProfileV1, privateKey, SignatureDomainOperation, payload)
	if err != nil {
		t.Fatal(err)
	}
	privateBytes := copySecret(t, privateKey)
	input := appendLengthPrefixed(nil, []byte(SignatureDomainOperation))
	input = append(input, payload...)
	want := ed25519.Sign(ed25519.PrivateKey(privateBytes), input)
	wipe(privateBytes)
	wipe(input)
	if !bytes.Equal(signature, want) {
		t.Fatalf("signature = %x, want %x", signature, want)
	}
	if err := VerifyCanonical(CryptoProfileV1, publicKey, SignatureDomainOperation, payload, signature); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCanonical(CryptoProfileV1, publicKey, SignatureDomainSnapshot, payload, signature); !errors.Is(err, ErrSignatureVerification) {
		t.Fatalf("cross-domain verification error = %v", err)
	}
	tamperedPayload := append([]byte(nil), payload...)
	tamperedPayload[len(tamperedPayload)-1] ^= 1
	if err := VerifyCanonical(CryptoProfileV1, publicKey, SignatureDomainOperation, tamperedPayload, signature); !errors.Is(err, ErrSignatureVerification) {
		t.Fatalf("tampered-payload verification error = %v", err)
	}
}

func TestEd25519ValidationAndGenerationFailures(t *testing.T) {
	if publicKey, privateKey, err := generateEd25519Key(failingIdentityReader{}); !errors.Is(err, ErrRandomSource) || publicKey != nil || privateKey != nil {
		t.Fatalf("generation failure public = %x, private = %v, error = %v", publicKey, privateKey, err)
	}
	publicKey, privateKey, err := generateEd25519Key(bytes.NewReader(sequentialBytes(1, ed25519.SeedSize)))
	if err != nil {
		t.Fatal(err)
	}
	defer privateKey.Close()
	payload := []byte{0x01}

	invalidInputs := []struct {
		profile string
		domain  SignatureDomain
		payload []byte
		want    error
	}{
		{profile: "beresta.crypto.v2", domain: SignatureDomainOperation, payload: payload, want: ErrUnsupportedCryptoProfile},
		{profile: CryptoProfileV1, domain: "unknown", payload: payload, want: ErrInvalidSignatureInput},
		{profile: CryptoProfileV1, domain: SignatureDomainOperation, payload: nil, want: ErrInvalidSignatureInput},
	}
	for _, test := range invalidInputs {
		if signature, err := SignCanonical(test.profile, privateKey, test.domain, test.payload); !errors.Is(err, test.want) || signature != nil {
			t.Fatalf("invalid input result = %x, error = %v", signature, err)
		}
	}
	if err := VerifyCanonical(CryptoProfileV1, publicKey[:31], SignatureDomainOperation, payload, make([]byte, Ed25519SignatureBytes)); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("invalid public key error = %v", err)
	}
	if err := VerifyCanonical(CryptoProfileV1, publicKey, SignatureDomainOperation, payload, make([]byte, Ed25519SignatureBytes-1)); !errors.Is(err, ErrSignatureVerification) {
		t.Fatalf("invalid signature length error = %v", err)
	}

	wrongSizePrivate := takeTestSecret(t, make([]byte, Ed25519PrivateKeyBytes-1))
	if signature, err := SignCanonical(CryptoProfileV1, wrongSizePrivate, SignatureDomainOperation, payload); !errors.Is(err, ErrInvalidPrivateKey) || signature != nil {
		t.Fatalf("wrong private key result = %x, error = %v", signature, err)
	}
	if wrongSizePrivate.Len() != 0 {
		t.Fatal("invalid private key was not wiped")
	}
}

func TestIdentityCompatibilityVectorValues(t *testing.T) {
	var vector identityCompatibilityVector
	fixturePath := filepath.Join("..", "..", "schema", "testdata", "v1", "crypto-identity.json")
	encoded, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &vector); err != nil {
		t.Fatal(err)
	}
	if vector.FixtureFormat != 1 || vector.CryptoProfile != CryptoProfileV1 {
		t.Fatalf("unsupported identity fixture header: format=%d profile=%q", vector.FixtureFormat, vector.CryptoProfile)
	}

	xRandom := decodeVectorHex(t, vector.X25519.KeyRandomHex)
	xPublic, xPrivate, err := generateX25519Identity(bytes.NewReader(xRandom))
	if err != nil {
		t.Fatal(err)
	}
	defer xPrivate.Close()
	if !bytes.Equal(xPublic, decodeVectorHex(t, vector.X25519.PublicKeyHex)) {
		t.Fatalf("X25519 public key = %x", xPublic)
	}
	xPrivateRaw := copySecret(t, xPrivate)
	defer wipe(xPrivateRaw)
	if !bytes.Equal(xPrivateRaw, decodeVectorHex(t, vector.X25519.PrivateKeyHex)) {
		t.Fatalf("X25519 private key = %x", xPrivateRaw)
	}
	payloadBytes := decodeVectorHex(t, vector.X25519.PlaintextHex)
	payload := takeTestSecret(t, payloadBytes)
	defer payload.Close()
	sealed, err := sealWorkspaceKeyEnvelope(CryptoProfileV1, xPublic, payload, bytes.NewReader(decodeVectorHex(t, vector.X25519.SealRandomHex)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sealed, decodeVectorHex(t, vector.X25519.SealedHex)) {
		t.Fatalf("sealed envelope = %x", sealed)
	}
	if len(sealed) <= X25519PublicKeyBytes {
		t.Fatal("sealed-box fixture is too short")
	}
	var ephemeralPublic, recipientPublic, recipientPrivate [X25519PublicKeyBytes]byte
	copy(ephemeralPublic[:], sealed[:X25519PublicKeyBytes])
	copy(recipientPublic[:], xPublic)
	copy(recipientPrivate[:], xPrivateRaw)
	hash, err := blake2b.New(XChaCha20NonceBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = hash.Write(ephemeralPublic[:])
	_, _ = hash.Write(recipientPublic[:])
	var nonce [XChaCha20NonceBytes]byte
	copy(nonce[:], hash.Sum(nil))
	referencePlaintext, ok := box.Open(nil, sealed[X25519PublicKeyBytes:], &nonce, &ephemeralPublic, &recipientPrivate)
	wipe(nonce[:])
	wipe(recipientPrivate[:])
	if !ok || !bytes.Equal(referencePlaintext, payloadBytes) {
		t.Fatalf("libsodium-format reference open = %x, ok=%v", referencePlaintext, ok)
	}
	wipe(referencePlaintext)

	edPublic, edPrivate, err := generateEd25519Key(bytes.NewReader(decodeVectorHex(t, vector.Ed25519.SeedHex)))
	if err != nil {
		t.Fatal(err)
	}
	defer edPrivate.Close()
	if !bytes.Equal(edPublic, decodeVectorHex(t, vector.Ed25519.PublicKeyHex)) {
		t.Fatalf("Ed25519 public key = %x", edPublic)
	}
	if got := copySecret(t, edPrivate); !bytes.Equal(got, decodeVectorHex(t, vector.Ed25519.PrivateKeyHex)) {
		t.Fatalf("Ed25519 private key = %x", got)
	}
	edPayload := decodeVectorHex(t, vector.Ed25519.PayloadHex)
	signature, err := SignCanonical(CryptoProfileV1, edPrivate, SignatureDomain(vector.Ed25519.Domain), edPayload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(signature, decodeVectorHex(t, vector.Ed25519.SignatureHex)) {
		t.Fatalf("Ed25519 signature = %x", signature)
	}
	if err := VerifyCanonical(CryptoProfileV1, edPublic, SignatureDomain(vector.Ed25519.Domain), edPayload, signature); err != nil {
		t.Fatal(err)
	}
}

type identityCompatibilityVector struct {
	FixtureFormat int    `json:"fixture_format"`
	CryptoProfile string `json:"crypto_profile"`
	X25519        struct {
		KeyRandomHex  string `json:"key_random_hex"`
		PublicKeyHex  string `json:"public_key_hex"`
		PrivateKeyHex string `json:"private_key_hex"`
		SealRandomHex string `json:"seal_random_hex"`
		PlaintextHex  string `json:"plaintext_hex"`
		SealedHex     string `json:"sealed_hex"`
	} `json:"x25519_sealed_box"`
	Ed25519 struct {
		SeedHex       string `json:"seed_hex"`
		PublicKeyHex  string `json:"public_key_hex"`
		PrivateKeyHex string `json:"private_key_hex"`
		Domain        string `json:"domain"`
		PayloadHex    string `json:"payload_hex"`
		SignatureHex  string `json:"signature_hex"`
	} `json:"ed25519_signature"`
}

func decodeVectorHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

type failingIdentityReader struct{}

func (failingIdentityReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("fixture random failure")
}

func sequentialBytes(first byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = first + byte(index)
	}
	return result
}

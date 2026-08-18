package crypto

import (
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/nacl/box"
)

const (
	X25519PublicKeyBytes   = 32
	X25519PrivateKeyBytes  = 32
	Ed25519PublicKeyBytes  = ed25519.PublicKeySize
	Ed25519PrivateKeyBytes = ed25519.PrivateKeySize
	Ed25519SignatureBytes  = ed25519.SignatureSize

	MaxWorkspaceKeyEnvelopePlaintextBytes = 64 * 1024
)

// SignatureDomain is a closed domain-separation label for signed canonical
// payloads. Each record class has an independent signature namespace.
type SignatureDomain string

const (
	SignatureDomainChallenge           SignatureDomain = "beresta.challenge.signature.v1"
	SignatureDomainOperation           SignatureDomain = "beresta.operation.signature.v1"
	SignatureDomainSnapshot            SignatureDomain = "beresta.snapshot.signature.v1"
	SignatureDomainMembership          SignatureDomain = "beresta.membership.signature.v1"
	SignatureDomainDeviceAuthorization SignatureDomain = "beresta.device-authorization.signature.v1"
	SignatureDomainRevocation          SignatureDomain = "beresta.revocation.signature.v1"
	SignatureDomainKeyTransition       SignatureDomain = "beresta.key-transition.signature.v1"
)

var (
	ErrInvalidPublicKey       = errors.New("crypto: invalid public key")
	ErrInvalidPrivateKey      = errors.New("crypto: invalid private key")
	ErrMalformedEnvelope      = errors.New("crypto: malformed envelope")
	ErrEnvelopeAuthentication = errors.New("crypto: envelope authentication failed")
	ErrSignatureVerification  = errors.New("crypto: signature verification failed")
	ErrRandomSource           = errors.New("crypto: random source failed")
	ErrInvalidSignatureInput  = errors.New("crypto: invalid signature input")
)

// GenerateX25519Identity creates an account encryption identity. The caller
// owns the returned private Secret and must close it.
func GenerateX25519Identity() ([]byte, *Secret, error) {
	return generateX25519Identity(cryptorand.Reader)
}

func generateX25519Identity(random io.Reader) ([]byte, *Secret, error) {
	if random == nil {
		return nil, nil, ErrRandomSource
	}
	publicKey, privateKey, err := box.GenerateKey(random)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: X25519 key generation", ErrRandomSource)
	}
	privateBytes := make([]byte, X25519PrivateKeyBytes)
	copy(privateBytes, privateKey[:])
	wipe(privateKey[:])
	private, err := TakeSecret(privateBytes)
	if err != nil {
		wipe(privateBytes)
		return nil, nil, err
	}
	publicBytes := append([]byte(nil), publicKey[:]...)
	wipe(publicKey[:])
	return publicBytes, private, nil
}

// SealWorkspaceKeyEnvelope seals a bounded canonical plaintext to an X25519
// recipient using the libsodium-compatible anonymous sealed-box construction.
// The plaintext remains owned by the caller and is not closed on success.
func SealWorkspaceKeyEnvelope(profile string, recipientPublicKey []byte, plaintext *Secret) ([]byte, error) {
	return sealWorkspaceKeyEnvelope(profile, recipientPublicKey, plaintext, cryptorand.Reader)
}

func sealWorkspaceKeyEnvelope(profile string, recipientPublicKey []byte, plaintext *Secret, random io.Reader) ([]byte, error) {
	if profile != CryptoProfileV1 {
		return nil, ErrUnsupportedCryptoProfile
	}
	recipient, err := x25519PublicKey(recipientPublicKey)
	if err != nil {
		return nil, err
	}
	if random == nil {
		return nil, ErrRandomSource
	}
	if plaintext == nil {
		return nil, ErrSecretClosed
	}
	if plaintext.Len() > MaxWorkspaceKeyEnvelopePlaintextBytes {
		return nil, ErrMalformedEnvelope
	}

	var sealed []byte
	var sealErr error
	err = plaintext.Use(func(value []byte) error {
		if len(value) == 0 || len(value) > MaxWorkspaceKeyEnvelopePlaintextBytes {
			return ErrMalformedEnvelope
		}
		sealed, sealErr = box.SealAnonymous(nil, value, &recipient, random)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if sealErr != nil {
		return nil, fmt.Errorf("%w: sealed-box nonce generation", ErrRandomSource)
	}
	return sealed, nil
}

// OpenWorkspaceKeyEnvelope authenticates and opens a sealed workspace-key
// payload. No plaintext is returned when validation or authentication fails.
func OpenWorkspaceKeyEnvelope(profile string, recipientPublicKey []byte, recipientPrivateKey *Secret, sealed []byte) (*Secret, error) {
	if profile != CryptoProfileV1 {
		return nil, ErrUnsupportedCryptoProfile
	}
	recipientPublic, err := x25519PublicKey(recipientPublicKey)
	if err != nil {
		return nil, err
	}
	if recipientPrivateKey == nil {
		return nil, ErrSecretClosed
	}
	if len(sealed) <= box.AnonymousOverhead || len(sealed) > MaxWorkspaceKeyEnvelopePlaintextBytes+box.AnonymousOverhead {
		return nil, ErrMalformedEnvelope
	}

	var plaintext []byte
	var opened bool
	err = recipientPrivateKey.Use(func(privateBytes []byte) error {
		if len(privateBytes) != X25519PrivateKeyBytes {
			return ErrInvalidPrivateKey
		}
		var recipientPrivate [X25519PrivateKeyBytes]byte
		copy(recipientPrivate[:], privateBytes)
		defer wipe(recipientPrivate[:])
		plaintext, opened = box.OpenAnonymous(nil, sealed, &recipientPublic, &recipientPrivate)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !opened {
		wipe(plaintext)
		return nil, ErrEnvelopeAuthentication
	}
	result, err := TakeSecret(plaintext)
	if err != nil {
		wipe(plaintext)
		return nil, err
	}
	return result, nil
}

// GenerateEd25519Key creates an independently revocable account-authority or
// device signing key. The caller owns and must close the private Secret.
func GenerateEd25519Key() ([]byte, *Secret, error) {
	return generateEd25519Key(cryptorand.Reader)
}

func generateEd25519Key(random io.Reader) ([]byte, *Secret, error) {
	if random == nil {
		return nil, nil, ErrRandomSource
	}
	publicKey, privateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: Ed25519 key generation", ErrRandomSource)
	}
	private, err := TakeSecret(privateKey)
	if err != nil {
		wipe(privateKey)
		return nil, nil, err
	}
	return append([]byte(nil), publicKey...), private, nil
}

// SignCanonical signs LP(domain) || canonicalPayload with an Ed25519 private
// key. Canonical payload validation belongs to the versioned schema codec.
func SignCanonical(profile string, privateKey *Secret, domain SignatureDomain, canonicalPayload []byte) ([]byte, error) {
	if err := validateSignatureInput(profile, domain, canonicalPayload); err != nil {
		return nil, err
	}
	if privateKey == nil {
		return nil, ErrSecretClosed
	}
	input := appendLengthPrefixed(nil, []byte(domain))
	input = append(input, canonicalPayload...)
	defer wipe(input)

	var signature []byte
	err := privateKey.Use(func(privateBytes []byte) error {
		if len(privateBytes) != Ed25519PrivateKeyBytes {
			return ErrInvalidPrivateKey
		}
		signature = ed25519.Sign(ed25519.PrivateKey(privateBytes), input)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return signature, nil
}

// VerifyCanonical verifies a domain-separated Ed25519 signature over a
// canonical payload.
func VerifyCanonical(profile string, publicKey []byte, domain SignatureDomain, canonicalPayload, signature []byte) error {
	if err := validateSignatureInput(profile, domain, canonicalPayload); err != nil {
		return err
	}
	if len(publicKey) != Ed25519PublicKeyBytes {
		return ErrInvalidPublicKey
	}
	if len(signature) != Ed25519SignatureBytes {
		return ErrSignatureVerification
	}
	input := appendLengthPrefixed(nil, []byte(domain))
	input = append(input, canonicalPayload...)
	defer wipe(input)
	if !ed25519.Verify(ed25519.PublicKey(publicKey), input, signature) {
		return ErrSignatureVerification
	}
	return nil
}

func validateSignatureInput(profile string, domain SignatureDomain, canonicalPayload []byte) error {
	if profile != CryptoProfileV1 {
		return ErrUnsupportedCryptoProfile
	}
	if !validSignatureDomain(domain) || len(canonicalPayload) == 0 || len(canonicalPayload) > MaxSecretBytes {
		return ErrInvalidSignatureInput
	}
	return nil
}

func validSignatureDomain(domain SignatureDomain) bool {
	switch domain {
	case SignatureDomainChallenge,
		SignatureDomainOperation,
		SignatureDomainSnapshot,
		SignatureDomainMembership,
		SignatureDomainDeviceAuthorization,
		SignatureDomainRevocation,
		SignatureDomainKeyTransition:
		return true
	default:
		return false
	}
}

func x25519PublicKey(value []byte) ([X25519PublicKeyBytes]byte, error) {
	var result [X25519PublicKeyBytes]byte
	if len(value) != X25519PublicKeyBytes {
		return result, ErrInvalidPublicKey
	}
	copy(result[:], value)
	return result, nil
}

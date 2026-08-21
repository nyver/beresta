package mobileapi

import (
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"regexp"
	"sync"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

var pairingCodePattern = regexp.MustCompile(`^[0-9]{6}$`)

const (
	pairingLifetime      = 5 * time.Minute
	maxPairingFrameBytes = 16 << 20
)

// PairingSession implements a balanced SPAKE2 exchange over P-256. The two
// fixed password elements are produced with deterministic try-and-increment
// hash-to-curve, so their discrete logarithms are not known. Both sides must
// verify the transcript confirmation before encrypted LAN frames are usable.
type PairingSession struct {
	mu        sync.Mutex
	initiator bool
	scalar    []byte
	password  []byte
	public    []byte
	peer      []byte
	key       *corecrypto.Secret
	verified  bool
	expires   time.Time
	seen      map[[chacha20poly1305.NonceSizeX]byte]struct{}
}

func NewPairingSession(role, shortCode string) (*PairingSession, error) {
	if role != "initiator" && role != "responder" {
		return nil, errors.New("mobileapi: pairing role must be initiator or responder")
	}
	if !pairingCodePattern.MatchString(shortCode) {
		return nil, errors.New("mobileapi: pairing short code must contain six digits")
	}
	curve := elliptic.P256()
	n := curve.Params().N
	x, err := rand.Int(rand.Reader, new(big.Int).Sub(n, big.NewInt(1)))
	if err != nil {
		return nil, err
	}
	x.Add(x, big.NewInt(1))
	wDigest := sha256.Sum256(append([]byte("beresta-spake2-password-v1\x00"), shortCode...))
	w := new(big.Int).SetBytes(wDigest[:])
	w.Mod(w, new(big.Int).Sub(n, big.NewInt(1)))
	w.Add(w, big.NewInt(1))
	mx, my := pairingElement("M")
	nx, ny := pairingElement("N")
	baseX, baseY := curve.ScalarBaseMult(x.Bytes())
	maskX, maskY := mx, my
	if role == "responder" {
		maskX, maskY = nx, ny
	}
	maskX, maskY = curve.ScalarMult(maskX, maskY, w.Bytes())
	publicX, publicY := curve.Add(baseX, baseY, maskX, maskY)
	return &PairingSession{initiator: role == "initiator", scalar: x.FillBytes(make([]byte, 32)), password: w.FillBytes(make([]byte, 32)),
		public: elliptic.Marshal(curve, publicX, publicY), expires: time.Now().Add(pairingLifetime), seen: make(map[[chacha20poly1305.NonceSizeX]byte]struct{})}, nil
}

func pairingElement(label string) (*big.Int, *big.Int) {
	curve := elliptic.P256()
	p := curve.Params().P
	three := big.NewInt(3)
	for counter := uint32(0); ; counter++ {
		hash := sha256.New()
		hash.Write([]byte("beresta-spake2-p256-" + label + "-v1"))
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], counter)
		hash.Write(encoded[:])
		x := new(big.Int).SetBytes(hash.Sum(nil))
		x.Mod(x, p)
		// P-256: y² = x³ - 3x + b (mod p).
		rhs := new(big.Int).Mul(x, x)
		rhs.Mul(rhs, x)
		rhs.Sub(rhs, new(big.Int).Mul(three, x))
		rhs.Add(rhs, curve.Params().B)
		rhs.Mod(rhs, p)
		y := new(big.Int).ModSqrt(rhs, p)
		if y == nil {
			continue
		}
		if y.Bit(0) != 0 {
			y.Sub(p, y)
		}
		return x, y
	}
}

func (p *PairingSession) PublicMessage() []byte {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.public...)
}

// Finish consumes the peer's SPAKE2 public message and returns this side's
// transcript confirmation. It does not expose the session key.
func (p *PairingSession) Finish(peerMessage []byte) ([]byte, error) {
	if p == nil {
		return nil, errors.New("mobileapi: pairing session is not available")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().After(p.expires) {
		p.closeLocked()
		return nil, errors.New("mobileapi: pairing session expired")
	}
	if p.key != nil || len(p.scalar) == 0 {
		return nil, errors.New("mobileapi: pairing session is not available")
	}
	curve := elliptic.P256()
	peerX, peerY := elliptic.Unmarshal(curve, peerMessage)
	if peerX == nil {
		return nil, errors.New("mobileapi: invalid pairing message")
	}
	mx, my := pairingElement("M")
	nx, ny := pairingElement("N")
	maskX, maskY := nx, ny
	if !p.initiator {
		maskX, maskY = mx, my
	}
	maskX, maskY = curve.ScalarMult(maskX, maskY, p.password)
	negY := new(big.Int).Neg(maskY)
	negY.Mod(negY, curve.Params().P)
	unmaskedX, unmaskedY := curve.Add(peerX, peerY, maskX, negY)
	if unmaskedX == nil || !curve.IsOnCurve(unmaskedX, unmaskedY) {
		return nil, errors.New("mobileapi: pairing password mismatch")
	}
	sharedX, _ := curve.ScalarMult(unmaskedX, unmaskedY, p.scalar)
	if sharedX == nil {
		return nil, errors.New("mobileapi: invalid pairing shared point")
	}
	p.peer = append([]byte(nil), peerMessage...)
	transcript := p.transcript()
	reader := hkdf.New(sha256.New, sharedX.FillBytes(make([]byte, 32)), transcript, []byte("beresta-lan-pairing-v1"))
	keyBytes := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(reader, keyBytes); err != nil {
		return nil, err
	}
	key, err := corecrypto.TakeSecret(keyBytes)
	if err != nil {
		return nil, err
	}
	p.key = key
	clear(p.scalar)
	clear(p.password)
	return p.confirmation(p.localLabel())
}

func (p *PairingSession) transcript() []byte {
	initiator, responder := p.public, p.peer
	if !p.initiator {
		initiator, responder = p.peer, p.public
	}
	digest := sha256.New()
	digest.Write([]byte("beresta-spake2-transcript-v1"))
	digest.Write(initiator)
	digest.Write(responder)
	return digest.Sum(nil)
}

func (p *PairingSession) localLabel() string {
	if p.initiator {
		return "initiator"
	}
	return "responder"
}

func (p *PairingSession) confirmation(label string) ([]byte, error) {
	if p.key == nil {
		return nil, errors.New("mobileapi: pairing exchange is incomplete")
	}
	var result []byte
	err := p.key.Use(func(key []byte) error {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte("beresta-spake2-confirm-" + label))
		mac.Write(p.transcript())
		result = mac.Sum(nil)
		return nil
	})
	return result, err
}

func (p *PairingSession) VerifyConfirmation(peerConfirmation []byte) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().After(p.expires) {
		p.closeLocked()
		return false
	}
	label := "responder"
	if !p.initiator {
		label = "initiator"
	}
	expected, err := p.confirmation(label)
	if err != nil || !hmac.Equal(expected, peerConfirmation) {
		p.closeLocked()
		return false
	}
	p.verified = true
	return true
}

func (p *PairingSession) Seal(plaintext []byte) ([]byte, error) {
	if p == nil {
		return nil, errors.New("mobileapi: pairing confirmation is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().After(p.expires) {
		p.closeLocked()
		return nil, errors.New("mobileapi: pairing session expired")
	}
	if !p.verified || p.key == nil || len(plaintext) > maxPairingFrameBytes {
		return nil, errors.New("mobileapi: pairing confirmation is required")
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	var result []byte
	err := p.key.Use(func(key []byte) error {
		aead, err := chacha20poly1305.NewX(key)
		if err != nil {
			return err
		}
		result = aead.Seal(nonce, nonce, plaintext, p.transcript())
		return nil
	})
	return result, err
}

func (p *PairingSession) Open(ciphertext []byte) ([]byte, error) {
	if p == nil {
		return nil, errors.New("mobileapi: invalid paired frame")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().After(p.expires) {
		p.closeLocked()
		return nil, errors.New("mobileapi: pairing session expired")
	}
	if !p.verified || p.key == nil || len(ciphertext) < chacha20poly1305.NonceSizeX+chacha20poly1305.Overhead || len(ciphertext) > maxPairingFrameBytes+chacha20poly1305.NonceSizeX+chacha20poly1305.Overhead {
		return nil, errors.New("mobileapi: invalid paired frame")
	}
	nonce := ciphertext[:chacha20poly1305.NonceSizeX]
	var nonceKey [chacha20poly1305.NonceSizeX]byte
	copy(nonceKey[:], nonce)
	if _, replayed := p.seen[nonceKey]; replayed {
		return nil, errors.New("mobileapi: paired frame replay")
	}
	var result []byte
	err := p.key.Use(func(key []byte) error {
		aead, err := chacha20poly1305.NewX(key)
		if err != nil {
			return err
		}
		result, err = aead.Open(nil, nonce, ciphertext[chacha20poly1305.NonceSizeX:], p.transcript())
		return err
	})
	if err == nil {
		p.seen[nonceKey] = struct{}{}
	}
	return result, err
}

func (p *PairingSession) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked()
}

func (p *PairingSession) closeLocked() {
	clear(p.scalar)
	clear(p.password)
	clear(p.public)
	clear(p.peer)
	clear(p.seen)
	if p.key != nil {
		p.key.Close()
	}
	p.key = nil
	p.verified = false
}

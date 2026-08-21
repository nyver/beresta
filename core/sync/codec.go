package sync

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
)

const (
	ProtocolV1                  = "beresta.sync.v1"
	SchemaVersionV1             = uint32(1)
	DefaultMaxOperationBytes    = 1 << 20
	HardMaxOperationBytes       = 16 << 20
	MaxOperationEnvelopeBytes   = HardMaxOperationBytes + 4096
	operationEnvelopeFieldCount = 10
)

var (
	ErrMalformedOperation    = errors.New("sync: malformed operation envelope")
	ErrUnsupportedVersion    = errors.New("sync: unsupported protocol version")
	ErrOperationSizeExceeded = errors.New("sync: operation size limit exceeded")
)

// WireOperation is the closed, signed operation envelope shared by clients
// and transports. Sequence is assigned by a transport and is deliberately
// excluded from the client signature.
type WireOperation struct {
	OpID        model.ID
	WorkspaceID model.ID
	DeviceID    model.ID
	Sequence    uint64
	Clock       model.HLC
	KeyID       []byte
	Nonce       []byte
	Ciphertext  []byte
	Signature   []byte
}

// CodecLimits bounds untrusted operation envelopes. Zero selects the safe
// protocol-v1 default; callers cannot raise the hard implementation ceiling.
type CodecLimits struct {
	MaxEnvelopeBytes   int
	MaxCiphertextBytes int
}

func (l CodecLimits) normalized() CodecLimits {
	if l.MaxCiphertextBytes <= 0 {
		l.MaxCiphertextBytes = DefaultMaxOperationBytes
	}
	if l.MaxCiphertextBytes > HardMaxOperationBytes {
		l.MaxCiphertextBytes = HardMaxOperationBytes
	}
	if l.MaxEnvelopeBytes <= 0 {
		l.MaxEnvelopeBytes = l.MaxCiphertextBytes + 4096
	}
	if l.MaxEnvelopeBytes > MaxOperationEnvelopeBytes {
		l.MaxEnvelopeBytes = MaxOperationEnvelopeBytes
	}
	return l
}

// NegotiateVersion selects protocol v1 when both peers advertise it.
func NegotiateVersion(peer []uint32) (uint32, error) {
	for _, version := range peer {
		if version == SchemaVersionV1 {
			return SchemaVersionV1, nil
		}
	}
	return 0, ErrUnsupportedVersion
}

// OperationSignatureInput returns the deterministic CBOR bytes covered by
// the Ed25519 operation signature.
func OperationSignatureInput(op WireOperation) ([]byte, error) {
	if err := validateWireOperation(op, false, DefaultMaxOperationBytes); err != nil {
		return nil, err
	}
	return corecrypto.CanonicalOperationSignatureInput(corecrypto.OperationSignatureFields{
		OpID: op.OpID.Bytes(), WorkspaceID: op.WorkspaceID.Bytes(), DeviceID: op.DeviceID.Bytes(),
		HLCPhysicalMS: op.Clock.PhysicalMS, HLCLogical: op.Clock.Logical, HLCDeviceID: op.Clock.DeviceID.Bytes(),
		KeyID: op.KeyID, Nonce: op.Nonce, Ciphertext: op.Ciphertext,
	})
}

// EncodeOperation emits the one accepted deterministic encoding. The
// server-stored form includes seq; a client-signed operation omits it.
func EncodeOperation(op WireOperation) ([]byte, error) {
	if err := validateWireOperation(op, true, HardMaxOperationBytes); err != nil {
		return nil, err
	}
	fieldCount := uint64(operationEnvelopeFieldCount)
	if op.Sequence != 0 {
		fieldCount++
	}
	out := appendCBORHead(nil, 5, fieldCount)
	out = appendCBORText(out, "protocol")
	out = appendCBORText(out, ProtocolV1)
	out = appendCBORText(out, "schema_version")
	out = appendCBORUint(out, uint64(SchemaVersionV1))
	out = appendCBORText(out, "op_id")
	out = appendCBORBytes(out, op.OpID.Bytes())
	out = appendCBORText(out, "workspace_id")
	out = appendCBORBytes(out, op.WorkspaceID.Bytes())
	out = appendCBORText(out, "device_id")
	out = appendCBORBytes(out, op.DeviceID.Bytes())
	if op.Sequence != 0 {
		out = appendCBORText(out, "seq")
		out = appendCBORUint(out, op.Sequence)
	}
	out = appendCBORText(out, "hlc")
	out = appendCBORHead(out, 5, 3)
	out = appendCBORText(out, "physical_ms")
	out = appendCBORUint(out, op.Clock.PhysicalMS)
	out = appendCBORText(out, "logical")
	out = appendCBORUint(out, uint64(op.Clock.Logical))
	out = appendCBORText(out, "device_id")
	out = appendCBORBytes(out, op.Clock.DeviceID.Bytes())
	out = appendCBORText(out, "key_id")
	out = appendCBORBytes(out, op.KeyID)
	out = appendCBORText(out, "nonce")
	out = appendCBORBytes(out, op.Nonce)
	out = appendCBORText(out, "ciphertext")
	out = appendCBORBytes(out, op.Ciphertext)
	out = appendCBORText(out, "sig")
	out = appendCBORBytes(out, op.Signature)
	return out, nil
}

// DecodeOperation strictly decodes an operation and verifies that re-encoding
// produces exactly the received bytes, which rejects every non-canonical form.
func DecodeOperation(data []byte, limits CodecLimits) (WireOperation, error) {
	limits = limits.normalized()
	if len(data) > limits.MaxEnvelopeBytes {
		return WireOperation{}, ErrOperationSizeExceeded
	}
	decoder := cborDecoder{data: data}
	fields, err := decoder.mapLength(operationEnvelopeFieldCount + 1)
	if err != nil || (fields != operationEnvelopeFieldCount && fields != operationEnvelopeFieldCount+1) {
		return WireOperation{}, wrapMalformed(err, "invalid envelope map")
	}
	var op WireOperation
	seen := make(map[string]struct{}, fields)
	var protocol string
	var schema uint64
	for index := uint64(0); index < fields; index++ {
		key, err := decoder.text(32)
		if err != nil {
			return WireOperation{}, wrapMalformed(err, "invalid field name")
		}
		if _, duplicate := seen[key]; duplicate {
			return WireOperation{}, fmt.Errorf("%w: duplicate field %q", ErrMalformedOperation, key)
		}
		seen[key] = struct{}{}
		switch key {
		case "protocol":
			protocol, err = decoder.text(32)
		case "schema_version":
			schema, err = decoder.uint()
		case "op_id":
			op.OpID, err = decoder.id()
		case "workspace_id":
			op.WorkspaceID, err = decoder.id()
		case "device_id":
			op.DeviceID, err = decoder.id()
		case "seq":
			op.Sequence, err = decoder.uint()
		case "hlc":
			op.Clock, err = decoder.hlc()
		case "key_id":
			op.KeyID, err = decoder.bytes(16)
			if err == nil && len(op.KeyID) != 16 {
				err = errors.New("key_id has invalid length")
			}
		case "nonce":
			op.Nonce, err = decoder.bytes(24)
			if err == nil && len(op.Nonce) != 24 {
				err = errors.New("nonce has invalid length")
			}
		case "ciphertext":
			op.Ciphertext, err = decoder.bytes(limits.MaxCiphertextBytes)
		case "sig":
			op.Signature, err = decoder.bytes(ed25519.SignatureSize)
			if err == nil && len(op.Signature) != ed25519.SignatureSize {
				err = errors.New("signature has invalid length")
			}
		default:
			return WireOperation{}, fmt.Errorf("%w: unknown mandatory field %q", ErrMalformedOperation, key)
		}
		if err != nil {
			return WireOperation{}, wrapMalformed(err, key)
		}
	}
	if decoder.offset != len(data) {
		return WireOperation{}, fmt.Errorf("%w: trailing bytes", ErrMalformedOperation)
	}
	if protocol != ProtocolV1 || schema != uint64(SchemaVersionV1) {
		return WireOperation{}, ErrUnsupportedVersion
	}
	required := []string{"protocol", "schema_version", "op_id", "workspace_id", "device_id", "hlc", "key_id", "nonce", "ciphertext", "sig"}
	for _, key := range required {
		if _, ok := seen[key]; !ok {
			return WireOperation{}, fmt.Errorf("%w: missing field %q", ErrMalformedOperation, key)
		}
	}
	if err := validateWireOperation(op, true, limits.MaxCiphertextBytes); err != nil {
		return WireOperation{}, err
	}
	canonical, err := EncodeOperation(op)
	if err != nil {
		return WireOperation{}, err
	}
	if !bytes.Equal(canonical, data) {
		return WireOperation{}, fmt.Errorf("%w: non-canonical encoding", ErrMalformedOperation)
	}
	return op, nil
}

func validateWireOperation(op WireOperation, requireSignature bool, maxCiphertext int) error {
	if err := op.OpID.Validate(); err != nil {
		return fmt.Errorf("%w: op_id", ErrMalformedOperation)
	}
	if err := op.WorkspaceID.Validate(); err != nil {
		return fmt.Errorf("%w: workspace_id", ErrMalformedOperation)
	}
	if err := op.DeviceID.Validate(); err != nil || op.Clock.DeviceID != op.DeviceID || op.Clock.PhysicalMS > math.MaxInt64 {
		return fmt.Errorf("%w: device or HLC", ErrMalformedOperation)
	}
	if len(op.KeyID) != 16 || len(op.Nonce) != 24 || len(op.Ciphertext) < corecrypto.AEADTagBytes {
		return fmt.Errorf("%w: invalid cryptographic field length", ErrMalformedOperation)
	}
	if len(op.Ciphertext) > maxCiphertext {
		return ErrOperationSizeExceeded
	}
	if requireSignature && len(op.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: invalid signature length", ErrMalformedOperation)
	}
	return nil
}

type cborDecoder struct {
	data   []byte
	offset int
}

func (d *cborDecoder) head(expectedMajor byte) (uint64, error) {
	if d.offset >= len(d.data) {
		return 0, errors.New("truncated CBOR item")
	}
	initial := d.data[d.offset]
	d.offset++
	major, additional := initial>>5, initial&31
	if major != expectedMajor || additional == 31 {
		return 0, errors.New("unexpected or indefinite CBOR item")
	}
	switch {
	case additional < 24:
		return uint64(additional), nil
	case additional == 24:
		value, err := d.takeUint(1)
		if err != nil || value < 24 {
			return 0, errors.New("non-minimal CBOR integer")
		}
		return value, nil
	case additional == 25:
		value, err := d.takeUint(2)
		if err != nil || value <= math.MaxUint8 {
			return 0, errors.New("non-minimal CBOR integer")
		}
		return value, nil
	case additional == 26:
		value, err := d.takeUint(4)
		if err != nil || value <= math.MaxUint16 {
			return 0, errors.New("non-minimal CBOR integer")
		}
		return value, nil
	case additional == 27:
		value, err := d.takeUint(8)
		if err != nil || value <= math.MaxUint32 {
			return 0, errors.New("non-minimal CBOR integer")
		}
		return value, nil
	default:
		return 0, errors.New("reserved CBOR additional information")
	}
}

func (d *cborDecoder) takeUint(size int) (uint64, error) {
	if size < 1 || d.offset+size > len(d.data) {
		return 0, errors.New("truncated CBOR integer")
	}
	var value uint64
	for _, b := range d.data[d.offset : d.offset+size] {
		value = value<<8 | uint64(b)
	}
	d.offset += size
	return value, nil
}

func (d *cborDecoder) uint() (uint64, error) { return d.head(0) }

func (d *cborDecoder) mapLength(max uint64) (uint64, error) {
	length, err := d.head(5)
	if err != nil || length > max {
		return 0, errors.New("invalid CBOR map length")
	}
	return length, nil
}

func (d *cborDecoder) bytes(max int) ([]byte, error) {
	length, err := d.head(2)
	if err != nil || length > uint64(max) || length > uint64(len(d.data)-d.offset) {
		return nil, errors.New("invalid CBOR byte string length")
	}
	result := append([]byte(nil), d.data[d.offset:d.offset+int(length)]...)
	d.offset += int(length)
	return result, nil
}

func (d *cborDecoder) text(max int) (string, error) {
	length, err := d.head(3)
	if err != nil || length > uint64(max) || length > uint64(len(d.data)-d.offset) {
		return "", errors.New("invalid CBOR text length")
	}
	value := d.data[d.offset : d.offset+int(length)]
	d.offset += int(length)
	if !utf8.Valid(value) {
		return "", errors.New("invalid UTF-8")
	}
	return string(value), nil
}

func (d *cborDecoder) id() (model.ID, error) {
	value, err := d.bytes(16)
	if err != nil || len(value) != 16 {
		return model.Nil, errors.New("invalid identifier")
	}
	return model.ParseID(value)
}

func (d *cborDecoder) hlc() (model.HLC, error) {
	length, err := d.mapLength(3)
	if err != nil || length != 3 {
		return model.HLC{}, errors.New("invalid HLC map")
	}
	var clock model.HLC
	seen := map[string]bool{}
	for index := uint64(0); index < length; index++ {
		key, err := d.text(16)
		if err != nil || seen[key] {
			return model.HLC{}, errors.New("invalid or duplicate HLC field")
		}
		seen[key] = true
		switch key {
		case "physical_ms":
			clock.PhysicalMS, err = d.uint()
		case "logical":
			value, uintErr := d.uint()
			if uintErr != nil || value > math.MaxUint32 {
				err = errors.New("invalid HLC logical value")
			} else {
				clock.Logical = uint32(value)
			}
		case "device_id":
			clock.DeviceID, err = d.id()
		default:
			return model.HLC{}, errors.New("unknown HLC field")
		}
		if err != nil {
			return model.HLC{}, err
		}
	}
	if !seen["physical_ms"] || !seen["logical"] || !seen["device_id"] {
		return model.HLC{}, errors.New("missing HLC field")
	}
	return clock, nil
}

func appendCBORText(dst []byte, value string) []byte {
	dst = appendCBORHead(dst, 3, uint64(len(value)))
	return append(dst, value...)
}

func appendCBORBytes(dst, value []byte) []byte {
	dst = appendCBORHead(dst, 2, uint64(len(value)))
	return append(dst, value...)
}

func appendCBORUint(dst []byte, value uint64) []byte { return appendCBORHead(dst, 0, value) }

func appendCBORHead(dst []byte, major byte, value uint64) []byte {
	switch {
	case value < 24:
		return append(dst, major<<5|byte(value))
	case value <= math.MaxUint8:
		return append(dst, major<<5|24, byte(value))
	case value <= math.MaxUint16:
		dst = append(dst, major<<5|25)
		return binary.BigEndian.AppendUint16(dst, uint16(value))
	case value <= math.MaxUint32:
		dst = append(dst, major<<5|26)
		return binary.BigEndian.AppendUint32(dst, uint32(value))
	default:
		dst = append(dst, major<<5|27)
		return binary.BigEndian.AppendUint64(dst, value)
	}
}

func wrapMalformed(err error, detail string) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrMalformedOperation, detail)
	}
	return fmt.Errorf("%w: %s: %v", ErrMalformedOperation, detail, err)
}

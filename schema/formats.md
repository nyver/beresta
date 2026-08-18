# Encoding formats

## Deterministic CBOR

Beresta v1 structures use deterministic CBOR following RFC 8949 section 4.2.1. Encoders and decoders must apply these additional profile rules:

- use the shortest integer and length encodings;
- use definite-length byte strings, text strings, arrays, and maps;
- order map keys by the bytewise lexicographic order of their deterministic CBOR encodings;
- reject duplicate map keys, invalid UTF-8, tags, floating-point values, simple values other than `false`, `true`, and schema-authorized `null`, trailing bytes, and integers outside the schema type;
- encode UUIDs and opaque identifiers as byte strings, never as UUID text;
- reconstruct signed and authenticated maps through the deterministic encoder;
- enforce a nesting depth of 32, at most 65,536 collection entries, and the smaller schema or configured byte limit before allocation.

All v1 maps are closed: an unrecognized key is invalid. A producer that needs another field must use a version that defines it. This avoids different implementations authenticating different semantic objects.

## Scalar notation

Schema documents use `bytes(N)` for exactly N bytes, `bytes(0..N)` for a bounded byte string, `text(0..N)` for a UTF-8 string bounded in encoded bytes, and `uintN` for the complete unsigned range of N bits. Array bounds are inclusive.

## JSON compatibility fixtures

JSON fixture files are transport-independent test descriptions, not alternate wire formats. Byte strings use lowercase hexadecimal without a prefix. A fixture has:

- `case`: a stable test name;
- `schema`: the versioned schema name;
- `valid`: the expected structural-validation result;
- `value`: the semantic value when valid, or the smallest invalid value needed by the case;
- `error`: the required stable error category for an invalid case.

Implementations decode fixture hex into bytes, construct their native value, deterministically encode it, decode it again, and compare the semantic result. Invalid cases must fail before cryptographic use. `canonical-primitives.json` additionally pins exact CBOR bytes for encoder bootstrap.

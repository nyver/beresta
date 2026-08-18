# ADR 0004: Use Deterministic CBOR for Versioned Envelopes

- Status: Accepted
- Date: 2026-08-16

## Context

Beresta needs compact binary envelopes for signed operations, HLC values, encrypted-object headers, attachment manifests, snapshots, cursor tokens, and compatibility fixtures. Cryptographic signatures and AEAD associated data require one unambiguous byte representation.

## Decision

Use deterministic CBOR with a strict schema profile defined in `schema/formats.md`. Canonical encoders create all signed and authenticated bytes. Decoders reject duplicate keys, indefinite lengths, non-minimal integers in signed material, invalid UTF-8, trailing bytes, unknown mandatory fields, excessive nesting, and configured size/count limits.

Use closed integer or text enums only where schema documents define them. Encode UUIDs and cryptographic material as fixed-length byte strings. Include protocol, schema, and crypto-profile versions in every independently stored envelope.

JSON test-vector files contain hexadecimal expected CBOR so humans can review inputs while implementations compare exact bytes.

## Consequences

- Signatures and AAD are deterministic across Go, TypeScript test tools, Dart test tools, and independent fixtures.
- Binary CRDT updates and ciphertext avoid base64 expansion inside wire envelopes.
- Strict decoding and resource limits require a configured CBOR implementation rather than permissive generic unmarshalling.
- Schema changes must distinguish optional fields from required semantics and preserve old vectors.

## Rejected Alternatives

### JSON

Rejected because key order, number representation, Unicode handling, duplicate keys, whitespace, and binary encoding make signed canonicalization easier to misuse and increase payload size.

### Protocol Buffers

Rejected because unknown-field preservation and deterministic serialization rules add generated-code and cross-language complexity for a small number of evolving envelope maps. Protobuf remains unsuitable as an implicit signature format without an additional canonical profile.

### MessagePack

Rejected because deterministic/canonical encoding conventions and schema tooling are less explicit for this use case.

### Custom binary format

Rejected because it would create avoidable parser and cryptographic canonicalization risk.

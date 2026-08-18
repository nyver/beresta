# Beresta schema

This directory contains the versioned, language-neutral wire and storage contracts shared by the Go, TypeScript, and Dart parts of Beresta.

- [`formats.md`](formats.md) defines deterministic CBOR and fixture notation.
- [`v1/`](v1/) contains the closed version-1 schemas.
- [`testdata/v1/`](testdata/v1/) contains structural compatibility fixtures. Cryptographic golden vectors are added separately when the cryptographic core exists.

An implementation must reject a value that violates a schema even when its CBOR is otherwise well formed. Changing a required field, field meaning, authenticated input, or byte representation requires a new schema or protocol version.

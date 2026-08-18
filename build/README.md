# Build assets

This directory contains reproducible build, packaging, CI, and deployment assets for Beresta clients and the optional server.

Generated outputs live in `output/`. Project-local Go caches and binding tools live in hidden `.go/` and `.go-cache/` directories so `go ./...` never traverses downloaded module sources. Gradle, Dart/Flutter state, pub packages, and optional portable tools such as w64devkit live in `.gradle/`, `.flutter/`, `.pub-cache/`, and `tools/` to avoid relying on writable global profile directories. All are ignored by Git.

# Vendored dependencies

`go-sqlcipher` is pinned to `github.com/AnoRebel/go-sqlcipher` v1.0.0. It is
copied from tagged commit `752605285aa21a216cfa5ab4f5cbe8b91e284f95` and
vendored because the upstream LibTomCrypt pointer-width assertion assumes the
LP64 data model. Windows amd64 uses LLP64, where pointers are 64-bit while
`unsigned long` is 32-bit. The one-line `_WIN64` condition in
`tomcrypt_private.h` selects the existing 64-bit literal type on that platform.

The upstream license is retained in `go-sqlcipher/LICENSE`.

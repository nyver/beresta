## go-sqlcipher

[![GoDoc](http://img.shields.io/badge/go-documentation-blue.svg?style=flat-square)](http://godoc.org/github.com/AnoRebel/go-sqlcipher) [![CI](https://github.com/AnoRebel/go-sqlcipher/workflows/CI/badge.svg)](https://github.com/AnoRebel/go-sqlcipher/actions)

### Description

Self-contained Go sqlite3 driver with an AES-256 encrypted sqlite3 database
conforming to the built-in database/sql interface. It is based on:

- Go sqlite3 driver: https://github.com/mattn/go-sqlite3 (v1.14.34)
- SQLite extension with AES-256 codec: https://github.com/sqlcipher/sqlcipher (v4.14.0, SQLite 3.51.3)
- AES-256 implementation from: https://github.com/libtom/libtomcrypt
- Hardware-accelerated AES from mbedTLS (AES-NI on x86_64, ARM Crypto Extensions on arm64)

SQLite itself is part of SQLCipher.

### Requirements

- Go 1.26+
- C compiler (gcc or clang) for CGo

### Incompatibilities of SQLCipher

**SQLCipher 4.x is incompatible with SQLCipher 3.x!**

go-sqlcipher supports a compatibility mode to open SQLCipher 3.x databases
(see `_pragma_cipher_compatibility` below).

See [migrating databases](https://www.zetetic.net/sqlcipher/sqlcipher-api/#Migrating_Databases) for details.

### Installation

This package can be installed with the go get command:

    go get github.com/AnoRebel/go-sqlcipher


### Documentation

To create and open encrypted database files use the following DSN parameters:

**Hex key (256-bit):**

```go
key := "2DD29CA851E7B56E4697B0E1F08507293D761A05CE4D1B628663F411A8086D99"
dbname := fmt.Sprintf("db?_pragma_key=x'%s'&_pragma_cipher_page_size=4096", key)
db, _ := sql.Open("sqlite3", dbname)
```

`_pragma_key` is the hex encoded 32 byte key (must be 64 characters long).
`_pragma_cipher_page_size` is the page size of the encrypted database (set if
you want a different value than the default size).

**Passphrase:**

```go
key := url.QueryEscape("secret")
dbname := fmt.Sprintf("db?_pragma_key=%s&_pragma_cipher_page_size=4096", key)
db, _ := sql.Open("sqlite3", dbname)
```

This uses a passphrase directly as `_pragma_key` with the key derivation function in
SQLCipher. Do not forget the `url.QueryEscape()` call in your code!

**SQLCipher 3 compatibility mode:**

```go
dbname := "db?_pragma_key=secret&_pragma_cipher_compatibility=3"
db, _ := sql.Open("sqlite3", dbname)
```

This opens a database created with SQLCipher 3.x using the v4 library.

See also [PRAGMA key](https://www.zetetic.net/sqlcipher/sqlcipher-api/#PRAGMA_key).

### DSN Parameters

| Parameter | Description |
|---|---|
| `_pragma_key` | Encryption key (hex with `x'...'` prefix, or passphrase via `url.QueryEscape()`) |
| `_pragma_cipher_page_size` | Page size for the encrypted database (default: SQLCipher default) |
| `_pragma_cipher_compatibility` | Set to `3` to open SQLCipher 3.x databases |

### Utility Functions

Use the function
[sqlite3.IsEncrypted()](https://godoc.org/github.com/AnoRebel/go-sqlcipher#IsEncrypted)
to check whether a database file is encrypted or not.

### Examples

Examples can be found under the `./_example` directory.

### License

The code of the originating packages is covered by their respective licenses.
See [LICENSE](LICENSE) file for details.

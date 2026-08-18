package sqlite3

/*
// make go-sqlite3 use embedded library without code changes
#cgo CFLAGS: -DUSE_LIBSQLITE3

// enable encryption codec in sqlite
#cgo CFLAGS: -DSQLITE_HAS_CODEC

// SQLCipher 4.7.0 Breaking Change
// Requires defining
//   `SQLITE_EXTRA_INIT=sqlcipher_extra_init` and
//   `SQLITE_EXTRA_SHUTDOWN=sqlcipher_extra_shutdown`
// at compile time for optimized library initialization and cleanup
#cgo CFLAGS: -DSQLITE_EXTRA_INIT=sqlcipher_extra_init
#cgo CFLAGS: -DSQLITE_EXTRA_SHUTDOWN=sqlcipher_extra_shutdown

// use memory for temporary storage in sqlite
#cgo CFLAGS: -DSQLITE_TEMP_STORE=2

// use libtomcrypt implementation in sqlcipher
#cgo CFLAGS: -DSQLCIPHER_CRYPTO_LIBTOMCRYPT

// disable anything "not portable" in libtomcrypt
#cgo CFLAGS: -DLTC_NO_ASM

// disable assertions
#cgo CFLAGS: -DNDEBUG

// ensure stdint.h is included for uint64_t etc.
#cgo CFLAGS: -DHAVE_STDINT_H

// set operating specific sqlite flags
#cgo linux CFLAGS: -DSQLITE_OS_UNIX=1
#cgo windows CFLAGS: -DSQLITE_OS_WIN=1
*/
import "C"

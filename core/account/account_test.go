package account

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
)

// fakeWrapper is a deterministic in-memory keystore.Wrapper for tests. It
// exercises the real BKW1 envelope format so metadata/protection binding is
// still verified, without depending on a platform keystore.
type fakeWrapper struct {
	protection keystore.Protection
}

func newFakeWrapper() *fakeWrapper { return &fakeWrapper{protection: keystore.ProtectionWindowsDPAPI} }

func (f *fakeWrapper) Protection() keystore.Protection { return f.protection }

func (f *fakeWrapper) Wrap(_ context.Context, metadata keystore.Metadata, secret *corecrypto.Secret) ([]byte, error) {
	var plaintext []byte
	if err := secret.Use(func(b []byte) error {
		plaintext = append([]byte(nil), b...)
		return nil
	}); err != nil {
		return nil, err
	}
	defer clear(plaintext)
	return keystore.SealEnvelope(f.protection, metadata, plaintext)
}

func (f *fakeWrapper) Unwrap(_ context.Context, metadata keystore.Metadata, encoded []byte) (*corecrypto.Secret, error) {
	plaintext, err := keystore.OpenEnvelope(encoded, f.protection, metadata)
	if err != nil {
		return nil, err
	}
	return corecrypto.TakeSecret(plaintext)
}

func (f *fakeWrapper) Delete(context.Context, keystore.Metadata) error { return nil }

var _ keystore.Wrapper = (*fakeWrapper)(nil)

func tempDBPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "beresta-account-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for attempt := 0; attempt < 10; attempt++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	return filepath.Join(dir, "beresta.db")
}

// fastKDF keeps Argon2id calibration well under the default 128 MiB/1s
// ceiling so tests run quickly.
func fastKDF() corecrypto.Argon2idCalibrationOptions {
	return corecrypto.Argon2idCalibrationOptions{MemoryLimitKiB: corecrypto.MinArgon2idMemoryKiB, Parallelism: 1}
}

func TestEraseLocalAccountRemovesEveryLocalFile(t *testing.T) {
	path := tempDBPath(t)
	wrapper := newFakeWrapper()
	ctx := context.Background()

	created, err := Create(ctx, CreateOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := created.Lock(); err != nil {
		t.Fatalf("Lock() error = %v", err)
	}

	// Simulate a populated attachment blob store without needing full
	// attachment machinery: EraseLocalAccount only needs these directories
	// (and anything in them) to exist and disappear.
	dataDir := filepath.Dir(path)
	blobsDir := filepath.Join(dataDir, "blobs")
	runtimeDir := filepath.Join(dataDir, "runtime")
	if err := os.MkdirAll(blobsDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(blobs) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(blobsDir, "some-blob"), []byte("ciphertext"), 0o600); err != nil {
		t.Fatalf("WriteFile(blob) error = %v", err)
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(runtime) error = %v", err)
	}

	envelope := envelopePath(path)
	for _, p := range []string{path, envelope} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("Stat(%s) error = %v, want the file to exist before erasing", p, err)
		}
	}

	if err := EraseLocalAccount(path); err != nil {
		t.Fatalf("EraseLocalAccount() error = %v", err)
	}

	for _, p := range []string{path, path + "-wal", path + "-shm", envelope, blobsDir, runtimeDir} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("Stat(%s) after EraseLocalAccount() error = %v, want os.IsNotExist", p, err)
		}
	}

	if _, err := Unlock(ctx, UnlockOptions{DatabasePath: path, Passphrase: []byte("correct horse battery staple"), Wrapper: wrapper}); !errors.Is(err, ErrNoLocalAccount) {
		t.Fatalf("Unlock() after erase error = %v, want %v", err, ErrNoLocalAccount)
	}
}

func TestEraseLocalAccountOnAnAlreadyMissingPathIsNotAnError(t *testing.T) {
	path := tempDBPath(t)
	if err := EraseLocalAccount(path); err != nil {
		t.Fatalf("EraseLocalAccount() on a never-created path error = %v, want nil", err)
	}
}

// TestEraseLocalAccountReportsAFailureToDeleteTheDatabase proves that a
// database file EraseLocalAccount cannot actually delete - here, because
// the caller violated the documented precondition and left the account
// unlocked, so the SQLCipher connection still holds the file open - is
// reported as an error rather than silently treated as erased. Without
// this, a caller (WipeLocalAccount) could report a wipe as successful and
// navigate away while the encrypted database was still fully intact on
// disk.
func TestEraseLocalAccountReportsAFailureToDeleteTheDatabase(t *testing.T) {
	path := tempDBPath(t)
	wrapper := newFakeWrapper()
	ctx := context.Background()

	created, err := Create(ctx, CreateOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	defer created.Lock()
	// Deliberately left unlocked: the open connection should keep the
	// database file locked open on Windows, so EraseLocalAccount's own
	// os.Remove of it fails instead of silently no-oping.

	if err := EraseLocalAccount(path); err == nil {
		t.Fatal("EraseLocalAccount() error = nil while the database is still open, want a non-nil error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat(path) after a failed EraseLocalAccount() error = %v, want the file to still exist", err)
	}
}

func TestCreateThenUnlockRecoversTheSameIdentity(t *testing.T) {
	path := tempDBPath(t)
	wrapper := newFakeWrapper()
	ctx := context.Background()

	created, err := Create(ctx, CreateOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	wantAccountID := created.ID
	wantDeviceID := created.DeviceID
	wantIdentityPub := append([]byte(nil), created.IdentityPublicKey...)
	wantAuthorityPub := append([]byte(nil), created.AuthorityPublicKey...)
	wantDevicePub := append([]byte(nil), created.DevicePublicKey...)
	if err := created.Lock(); err != nil {
		t.Fatalf("Lock() error = %v", err)
	}

	unlocked, err := Unlock(ctx, UnlockOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
	})
	if err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	defer unlocked.Lock()

	if unlocked.ID != wantAccountID {
		t.Fatalf("unlocked.ID = %s, want %s", unlocked.ID, wantAccountID)
	}
	if unlocked.DeviceID != wantDeviceID {
		t.Fatalf("unlocked.DeviceID = %s, want %s", unlocked.DeviceID, wantDeviceID)
	}
	if !bytes.Equal(unlocked.IdentityPublicKey, wantIdentityPub) {
		t.Fatal("identity public key changed across unlock")
	}
	if !bytes.Equal(unlocked.AuthorityPublicKey, wantAuthorityPub) {
		t.Fatal("authority public key changed across unlock")
	}
	if !bytes.Equal(unlocked.DevicePublicKey, wantDevicePub) {
		t.Fatal("device public key changed across unlock")
	}
}

func TestUnlockedWorkspaceKeyIsUsable(t *testing.T) {
	path := tempDBPath(t)
	wrapper := newFakeWrapper()
	ctx := context.Background()

	created, err := Create(ctx, CreateOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var workspaceID [16]byte
	var found bool
	for id := range created.workspaceKeys {
		workspaceID = id
		found = true
		break
	}
	if !found {
		t.Fatal("Create() produced no workspace key")
	}
	key, keyID, err := created.WorkspaceKey(workspaceID)
	if err != nil {
		t.Fatalf("WorkspaceKey() error = %v", err)
	}
	if len(keyID) != workspaceKeyIDLen {
		t.Fatalf("key ID length = %d, want %d", len(keyID), workspaceKeyIDLen)
	}
	if key.Len() != workspaceKeyBytes {
		t.Fatalf("key length = %d, want %d", key.Len(), workspaceKeyBytes)
	}
	created.Lock()
}

func TestUnlockRejectsWrongPassphrase(t *testing.T) {
	path := tempDBPath(t)
	wrapper := newFakeWrapper()
	ctx := context.Background()

	created, err := Create(ctx, CreateOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatal(err)
	}
	created.Lock()

	if _, err := Unlock(ctx, UnlockOptions{
		DatabasePath: path,
		Passphrase:   []byte("wrong passphrase entirely"),
		Wrapper:      wrapper,
	}); !errors.Is(err, corecrypto.ErrKeybagUnlock) {
		t.Fatalf("Unlock() with wrong passphrase error = %v, want ErrKeybagUnlock", err)
	}
}

func TestUnlockRejectsMissingAccount(t *testing.T) {
	path := tempDBPath(t)
	wrapper := newFakeWrapper()
	if _, err := Unlock(context.Background(), UnlockOptions{
		DatabasePath: path,
		Passphrase:   []byte("whatever"),
		Wrapper:      wrapper,
	}); !errors.Is(err, ErrNoLocalAccount) {
		t.Fatalf("Unlock() on a missing account error = %v, want ErrNoLocalAccount", err)
	}
}

func TestCreateRejectsExistingAccount(t *testing.T) {
	path := tempDBPath(t)
	wrapper := newFakeWrapper()
	ctx := context.Background()
	opts := CreateOptions{DatabasePath: path, Passphrase: []byte("passphrase one"), Wrapper: wrapper, KDFOptions: fastKDF()}
	created, err := Create(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	created.Lock()

	if _, err := Create(ctx, CreateOptions{DatabasePath: path, Passphrase: []byte("passphrase two"), Wrapper: wrapper, KDFOptions: fastKDF()}); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("second Create() error = %v, want ErrAccountExists", err)
	}
}

func TestCreateWipesPassphrase(t *testing.T) {
	path := tempDBPath(t)
	wrapper := newFakeWrapper()
	passphrase := []byte("correct horse battery staple")
	created, err := Create(context.Background(), CreateOptions{
		DatabasePath: path,
		Passphrase:   passphrase,
		Wrapper:      wrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer created.Lock()
	for _, b := range passphrase {
		if b != 0 {
			t.Fatal("Create() did not wipe the caller's passphrase buffer")
		}
	}
}

func TestLockIsIdempotentAndWipesSecrets(t *testing.T) {
	path := tempDBPath(t)
	wrapper := newFakeWrapper()
	created, err := Create(context.Background(), CreateOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Lock(); err != nil {
		t.Fatalf("first Lock() error = %v", err)
	}
	if err := created.Lock(); err != nil {
		t.Fatalf("second Lock() error = %v", err)
	}
	if created.DB() != nil {
		t.Fatal("DB() must be nil after Lock()")
	}
	var zero [16]byte
	if _, _, err := created.WorkspaceKey(zero); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("WorkspaceKey() after Lock() error = %v, want ErrAccountLocked", err)
	}
}

func TestCreateRequiresOptions(t *testing.T) {
	ctx := context.Background()
	wrapper := newFakeWrapper()
	path := tempDBPath(t)

	if _, err := Create(ctx, CreateOptions{Passphrase: []byte("x"), Wrapper: wrapper}); err == nil {
		t.Fatal("expected an error for a missing database path")
	}
	if _, err := Create(ctx, CreateOptions{DatabasePath: path, Wrapper: wrapper}); err == nil {
		t.Fatal("expected an error for a missing passphrase")
	}
	if _, err := Create(ctx, CreateOptions{DatabasePath: path, Passphrase: []byte("x")}); !errors.Is(err, keystore.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable for a missing wrapper", err)
	}
}

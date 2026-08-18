package account

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

const (
	// localDeviceKeyID is the fixed keystore KeyID for this device's own
	// wrapped secrets. It must be knowable before the encrypted database can
	// be opened, so it cannot be the device's own UUID (which is stored only
	// inside that database); Purpose distinguishes the database key from the
	// device signing key under the same KeyID.
	localDeviceKeyID = "local-device"
	// devicePrivateKeyPurpose is the keystore Purpose for the wrapped
	// per-device Ed25519 signing private key.
	devicePrivateKeyPurpose = "device-signing-key"

	envelopeFileSuffix   = ".keyenvelope"
	workspaceKeyIDLen    = 16
	initialKeybagVersion = 1
)

var (
	// ErrAccountExists reports that Create was called against a path that
	// already has a database or key envelope.
	ErrAccountExists = errors.New("account: an account already exists at this path")
	// ErrNoLocalAccount reports that Unlock was called against a path with
	// no local key envelope.
	ErrNoLocalAccount = errors.New("account: no local account at this path")
	// ErrAccountLocked reports use of an Account after Lock.
	ErrAccountLocked = errors.New("account: account is locked")
	// ErrUnknownWorkspace reports a workspace ID with no key held by this
	// unlocked account.
	ErrUnknownWorkspace = errors.New("account: unknown workspace")
)

// workspaceKeyEntry is one workspace key held live in memory by an unlocked
// Account.
type workspaceKeyEntry struct {
	KeyID []byte
	Key   *corecrypto.Secret
	State workspaceKeyState
}

// Account is an unlocked local account and its live cryptographic session.
// Every field named *PublicKey is safe to log or display; every private key
// and workspace key is held as an owned Secret and is wiped by Lock.
type Account struct {
	ID                 model.ID
	DeviceID           model.ID
	IdentityPublicKey  []byte // X25519
	AuthorityPublicKey []byte // Ed25519 account authority key
	DevicePublicKey    []byte // Ed25519 this device's signing key

	mu               sync.Mutex
	locked           bool
	db               *sql.DB
	identityPrivate  *corecrypto.Secret
	authorityPrivate *corecrypto.Secret
	devicePrivate    *corecrypto.Secret
	// rootKey is retained for the account's unlocked lifetime (unlike a
	// transient keybag-unlock derivation) because backup creation derives a
	// fresh per-backup key from it on demand; see core/account/backup.go.
	rootKey       *corecrypto.Secret
	workspaceKeys map[model.ID]workspaceKeyEntry
	clock         *model.Clock
	blobs         *store.BlobStore
}

// workspaceSession snapshots what one workspace-scoped local mutation needs
// under a single lock acquisition: the open database, this device's
// identity and signing key, and the requested workspace's current key
// entry. It performs no I/O beyond the in-memory read.
func (a *Account) workspaceSession(workspaceID model.ID) (db *sql.DB, entry workspaceKeyEntry, deviceID model.ID, devicePrivate *corecrypto.Secret, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locked {
		return nil, workspaceKeyEntry{}, model.ID{}, nil, ErrAccountLocked
	}
	entry, ok := a.workspaceKeys[workspaceID]
	if !ok {
		return nil, workspaceKeyEntry{}, model.ID{}, nil, ErrUnknownWorkspace
	}
	return a.db, entry, a.DeviceID, a.devicePrivate, nil
}

// tick issues this device's next Hybrid Logical Clock value for a new local
// event. The caller must persist it (see store.AdvanceDeviceClock) in the
// same transaction as the event it timestamps.
func (a *Account) tick() (model.HLC, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locked {
		return model.HLC{}, ErrAccountLocked
	}
	return a.clock.Tick()
}

// DB returns the account's open database connection. It becomes invalid
// after Lock.
func (a *Account) DB() *sql.DB {
	return a.db
}

// WorkspaceKey returns the current key material for workspaceID. The
// returned Secret is owned by the Account: callers must not close it, and it
// stops being valid once Lock runs.
func (a *Account) WorkspaceKey(workspaceID model.ID) (key *corecrypto.Secret, keyID []byte, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locked {
		return nil, nil, ErrAccountLocked
	}
	entry, ok := a.workspaceKeys[workspaceID]
	if !ok {
		return nil, nil, fmt.Errorf("account: unknown workspace")
	}
	return entry.Key, entry.KeyID, nil
}

// Lock wipes every live secret and closes the database connection. It is
// idempotent.
func (a *Account) Lock() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locked {
		return nil
	}
	a.identityPrivate.Close()
	a.authorityPrivate.Close()
	a.devicePrivate.Close()
	a.rootKey.Close()
	for _, entry := range a.workspaceKeys {
		entry.Key.Close()
	}
	a.workspaceKeys = nil

	var closeErr error
	if a.db != nil {
		closeErr = a.db.Close()
		a.db = nil
	}
	a.locked = true
	return closeErr
}

// CreateOptions configures a new local account.
type CreateOptions struct {
	// DatabasePath is where the new encrypted client database is created.
	DatabasePath string
	// Passphrase is the caller-owned account passphrase. Create wipes it
	// before returning, whether or not it succeeds.
	Passphrase []byte
	// Wrapper is the platform OS-keystore adapter that wraps the local
	// database key and device signing key.
	Wrapper keystore.Wrapper
	// KDFOptions bounds Argon2id calibration for this device. The zero
	// value selects the reviewed default ceiling.
	KDFOptions corecrypto.Argon2idCalibrationOptions
}

// Create generates a brand-new local account entirely on-device: the X25519
// identity, Ed25519 account authority key, this device's Ed25519 signing
// key, a random OS-keystore-wrapped database key, and the initial personal
// workspace and its key. It makes no network access.
func Create(ctx context.Context, opts CreateOptions) (*Account, error) {
	if opts.DatabasePath == "" {
		return nil, errors.New("account: database path is required")
	}
	if len(opts.Passphrase) == 0 {
		return nil, errors.New("account: passphrase is required")
	}
	if opts.Wrapper == nil {
		return nil, keystore.ErrUnavailable
	}
	defer clear(opts.Passphrase)

	if _, err := os.Stat(opts.DatabasePath); err == nil {
		return nil, ErrAccountExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	envelopeFile := envelopePath(opts.DatabasePath)
	if _, err := os.Stat(envelopeFile); err == nil {
		return nil, ErrAccountExists
	}

	accountID, err := model.NewID()
	if err != nil {
		return nil, err
	}
	deviceID, err := model.NewID()
	if err != nil {
		return nil, err
	}
	workspaceID, err := model.NewID()
	if err != nil {
		return nil, err
	}
	clock, err := model.NewClock(deviceID, model.HLC{}, 0)
	if err != nil {
		return nil, err
	}
	createdClock, err := clock.Tick()
	if err != nil {
		return nil, err
	}

	dbKey, dbEnvelope, err := store.LoadOrCreateDatabaseKey(ctx, opts.Wrapper, localDeviceKeyID, nil)
	if err != nil {
		return nil, err
	}
	// Persist the key envelope before opening the database, so a crash
	// between the two never leaves an encrypted database with no recorded
	// way to recover its key.
	if err := os.WriteFile(envelopeFile, dbEnvelope, 0o600); err != nil {
		dbKey.Close()
		return nil, fmt.Errorf("account: write key envelope: %w", err)
	}

	db, _, err := store.Open(ctx, opts.DatabasePath, dbKey)
	dbKey.Close()
	if err != nil {
		os.Remove(envelopeFile)
		return nil, err
	}

	account, err := createAccountContent(ctx, db, opts, accountID, deviceID, workspaceID, createdClock)
	if err != nil {
		db.Close()
		removeDatabaseFiles(opts.DatabasePath)
		os.Remove(envelopeFile)
		return nil, err
	}
	return account, nil
}

func createAccountContent(
	ctx context.Context,
	db *sql.DB,
	opts CreateOptions,
	accountID, deviceID, workspaceID model.ID,
	createdClock model.HLC,
) (*Account, error) {
	identityPub, identityPriv, err := corecrypto.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	authorityPub, authorityPriv, err := corecrypto.GenerateEd25519Key()
	if err != nil {
		identityPriv.Close()
		return nil, err
	}
	devicePub, devicePriv, err := corecrypto.GenerateEd25519Key()
	if err != nil {
		identityPriv.Close()
		authorityPriv.Close()
		return nil, err
	}

	signingEnvelope, err := opts.Wrapper.Wrap(ctx, keystore.Metadata{KeyID: localDeviceKeyID, Purpose: devicePrivateKeyPurpose}, devicePriv)
	if err != nil {
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		return nil, err
	}

	workspaceKeyID := make([]byte, workspaceKeyIDLen)
	if _, err := io.ReadFull(cryptorand.Reader, workspaceKeyID); err != nil {
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		return nil, fmt.Errorf("account: generate workspace key ID: %w", err)
	}
	workspaceKeyBytesRaw := make([]byte, workspaceKeyBytes)
	if _, err := io.ReadFull(cryptorand.Reader, workspaceKeyBytesRaw); err != nil {
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		return nil, fmt.Errorf("account: generate workspace key: %w", err)
	}
	workspaceKey, err := corecrypto.TakeSecret(workspaceKeyBytesRaw)
	if err != nil {
		clear(workspaceKeyBytesRaw)
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		return nil, err
	}

	kdfParams, err := corecrypto.CalibrateArgon2id(ctx, opts.KDFOptions)
	if err != nil {
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		workspaceKey.Close()
		return nil, err
	}
	rootKey, err := corecrypto.DeriveRootKey(ctx, opts.Passphrase, kdfParams)
	if err != nil {
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		workspaceKey.Close()
		return nil, err
	}
	header, err := corecrypto.NewKeybagHeader(accountID.Bytes(), initialKeybagVersion, kdfParams)
	if err != nil {
		rootKey.Close()
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		workspaceKey.Close()
		return nil, err
	}

	payload := keybagPlaintext{
		IdentityPublicKey:   identityPub,
		IdentityPrivateKey:  identityPriv,
		AuthorityPublicKey:  authorityPub,
		AuthorityPrivateKey: authorityPriv,
		WorkspaceKeys: []keybagWorkspaceKey{{
			WorkspaceID: workspaceID,
			KeyID:       workspaceKeyID,
			Key:         workspaceKey,
			State:       workspaceKeyStateCurrent,
		}},
	}
	payloadSecret, err := encodeKeybagPlaintext(payload)
	if err != nil {
		rootKey.Close()
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		workspaceKey.Close()
		return nil, err
	}
	encryptedKeybag, err := corecrypto.EncryptKeybag(rootKey, header, payloadSecret)
	payloadSecret.Close()
	if err != nil {
		rootKey.Close()
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		workspaceKey.Close()
		return nil, err
	}

	if err := insertNewAccount(ctx, db, insertAccountParams{
		accountID:       accountID,
		deviceID:        deviceID,
		workspaceID:     workspaceID,
		workspaceKeyID:  workspaceKeyID,
		devicePublicKey: devicePub,
		signingEnvelope: signingEnvelope,
		identityPublic:  identityPub,
		authorityPublic: authorityPub,
		keybag:          encryptedKeybag,
		createdClock:    createdClock,
	}); err != nil {
		rootKey.Close()
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		workspaceKey.Close()
		return nil, err
	}

	clock, err := model.NewClock(deviceID, createdClock, 0)
	if err != nil {
		rootKey.Close()
		identityPriv.Close()
		authorityPriv.Close()
		devicePriv.Close()
		workspaceKey.Close()
		return nil, err
	}

	return &Account{
		ID:                 accountID,
		DeviceID:           deviceID,
		IdentityPublicKey:  identityPub,
		AuthorityPublicKey: authorityPub,
		DevicePublicKey:    devicePub,
		db:                 db,
		identityPrivate:    identityPriv,
		authorityPrivate:   authorityPriv,
		devicePrivate:      devicePriv,
		rootKey:            rootKey,
		workspaceKeys: map[model.ID]workspaceKeyEntry{
			workspaceID: {KeyID: workspaceKeyID, Key: workspaceKey, State: workspaceKeyStateCurrent},
		},
		clock: clock,
		blobs: newBlobStore(opts.DatabasePath),
	}, nil
}

type insertAccountParams struct {
	accountID       model.ID
	deviceID        model.ID
	workspaceID     model.ID
	workspaceKeyID  []byte
	devicePublicKey []byte
	signingEnvelope []byte
	identityPublic  []byte
	authorityPublic []byte
	keybag          corecrypto.EncryptedKeybag
	createdClock    model.HLC
}

func insertNewAccount(ctx context.Context, db *sql.DB, p insertAccountParams) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin creation transaction: %w", err)
	}
	defer tx.Rollback()

	nowMS := p.createdClock.PhysicalMS
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO accounts (id, x25519_public_key, ed25519_public_key, keybag_version, keybag_nonce, keybag_ciphertext, kdf_salt, kdf_memory_kib, kdf_time_cost, kdf_parallelism, created_unix_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.accountID.Bytes(), p.identityPublic, p.authorityPublic, p.keybag.Header.KeybagVersion,
		p.keybag.Nonce, p.keybag.Ciphertext,
		p.keybag.Header.KDF.Salt, p.keybag.Header.KDF.MemoryKiB, p.keybag.Header.KDF.TimeCost, p.keybag.Header.KDF.Parallelism,
		nowMS,
	); err != nil {
		return fmt.Errorf("account: insert account row: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO devices (id, account_id, public_key, signing_key_envelope, status, is_local, created_physical_ms, created_logical, created_device_id, clock_physical_ms, clock_logical)
		 VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		p.deviceID.Bytes(), p.accountID.Bytes(), p.devicePublicKey, p.signingEnvelope, model.DeviceStatusActive,
		p.createdClock.PhysicalMS, p.createdClock.Logical, p.createdClock.DeviceID.Bytes(),
		p.createdClock.PhysicalMS, p.createdClock.Logical,
	); err != nil {
		return fmt.Errorf("account: insert device row: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspaces (id, created_physical_ms, created_logical, created_device_id) VALUES (?, ?, ?, ?)`,
		p.workspaceID.Bytes(), p.createdClock.PhysicalMS, p.createdClock.Logical, p.createdClock.DeviceID.Bytes(),
	); err != nil {
		return fmt.Errorf("account: insert workspace row: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspace_keys (key_id, workspace_id, state, activated_physical_ms, activated_logical, activated_device_id) VALUES (?, ?, ?, ?, ?, ?)`,
		p.workspaceKeyID, p.workspaceID.Bytes(), workspaceKeyStateCurrent,
		p.createdClock.PhysicalMS, p.createdClock.Logical, p.createdClock.DeviceID.Bytes(),
	); err != nil {
		return fmt.Errorf("account: insert workspace key row: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("account: commit creation transaction: %w", err)
	}
	return nil
}

// UnlockOptions configures unlocking an existing local account.
type UnlockOptions struct {
	// DatabasePath is the existing encrypted client database.
	DatabasePath string
	// Passphrase is the caller-owned account passphrase. Unlock wipes it
	// before returning, whether or not it succeeds.
	Passphrase []byte
	// Wrapper is the platform OS-keystore adapter that unwraps the local
	// database key and device signing key.
	Wrapper keystore.Wrapper
}

// Unlock opens an existing local account: it unwraps the database key
// through the platform keystore, opens and migrates the database, unwraps
// this device's signing key, and derives the Root Key from Passphrase to
// open the keybag. It makes no network access. A wrong passphrase and a
// corrupt keybag are reported through the same uniform error from
// corecrypto.UnlockKeybag.
func Unlock(ctx context.Context, opts UnlockOptions) (*Account, error) {
	if opts.DatabasePath == "" {
		return nil, errors.New("account: database path is required")
	}
	if len(opts.Passphrase) == 0 {
		return nil, errors.New("account: passphrase is required")
	}
	if opts.Wrapper == nil {
		return nil, keystore.ErrUnavailable
	}
	defer clear(opts.Passphrase)

	envelope, err := os.ReadFile(envelopePath(opts.DatabasePath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoLocalAccount
		}
		return nil, err
	}

	dbKey, _, err := store.LoadOrCreateDatabaseKey(ctx, opts.Wrapper, localDeviceKeyID, envelope)
	if err != nil {
		return nil, err
	}
	db, _, err := store.Open(ctx, opts.DatabasePath, dbKey)
	dbKey.Close()
	if err != nil {
		return nil, err
	}

	account, err := unlockAccountContent(ctx, db, opts)
	if err != nil {
		db.Close()
		return nil, err
	}
	return account, nil
}

func unlockAccountContent(ctx context.Context, db *sql.DB, opts UnlockOptions) (*Account, error) {
	accountRow, err := loadAccountRow(ctx, db)
	if err != nil {
		return nil, err
	}
	deviceRow, err := loadLocalDeviceRow(ctx, db)
	if err != nil {
		return nil, err
	}

	devicePrivate, err := opts.Wrapper.Unwrap(ctx, keystore.Metadata{KeyID: localDeviceKeyID, Purpose: devicePrivateKeyPurpose}, deviceRow.signingKeyEnvelope)
	if err != nil {
		return nil, err
	}

	kdfParams := corecrypto.Argon2idParams{
		CryptoProfile:   corecrypto.CryptoProfileV1,
		Algorithm:       corecrypto.Argon2idName,
		Salt:            accountRow.kdfSalt,
		MemoryKiB:       accountRow.kdfMemoryKiB,
		TimeCost:        accountRow.kdfTimeCost,
		Parallelism:     accountRow.kdfParallelism,
		DerivedKeyBytes: corecrypto.RootKeyBytes,
	}
	header, err := corecrypto.NewKeybagHeader(accountRow.id.Bytes(), accountRow.keybagVersion, kdfParams)
	if err != nil {
		devicePrivate.Close()
		return nil, err
	}
	encryptedKeybag := corecrypto.EncryptedKeybag{
		Header:     header,
		Nonce:      accountRow.keybagNonce,
		Ciphertext: accountRow.keybagCiphertext,
	}

	// Derive the Root Key directly (rather than through the UnlockKeybag
	// convenience wrapper, which derives and discards it internally) so it
	// can be retained on the returned Account: backup creation (see
	// core/account/backup.go) derives a fresh per-backup key from it later,
	// on demand, exactly as workspace/identity keys are already retained
	// for the account's unlocked lifetime. OpenKeybag reports the same
	// uniform error for a wrong passphrase and a corrupt/tampered keybag as
	// UnlockKeybag does, since UnlockKeybag is just these two calls.
	rootKey, err := corecrypto.DeriveRootKey(ctx, opts.Passphrase, kdfParams)
	if err != nil {
		devicePrivate.Close()
		return nil, err
	}
	keybagSecret, err := corecrypto.OpenKeybag(rootKey, encryptedKeybag)
	if err != nil {
		rootKey.Close()
		devicePrivate.Close()
		return nil, err
	}
	payload, err := decodeKeybagPlaintext(keybagSecret)
	keybagSecret.Close()
	if err != nil {
		rootKey.Close()
		devicePrivate.Close()
		return nil, err
	}

	workspaceKeys := make(map[model.ID]workspaceKeyEntry, len(payload.WorkspaceKeys))
	for _, wk := range payload.WorkspaceKeys {
		workspaceKeys[wk.WorkspaceID] = workspaceKeyEntry{KeyID: wk.KeyID, Key: wk.Key, State: wk.State}
	}

	persistedClock, err := store.LoadDeviceClock(ctx, db, deviceRow.id)
	if err != nil {
		rootKey.Close()
		devicePrivate.Close()
		closeUnlockedSecrets(payload, workspaceKeys)
		return nil, err
	}
	clock, err := model.NewClock(deviceRow.id, persistedClock, 0)
	if err != nil {
		rootKey.Close()
		devicePrivate.Close()
		closeUnlockedSecrets(payload, workspaceKeys)
		return nil, err
	}

	return &Account{
		ID:                 accountRow.id,
		DeviceID:           deviceRow.id,
		IdentityPublicKey:  payload.IdentityPublicKey,
		AuthorityPublicKey: payload.AuthorityPublicKey,
		DevicePublicKey:    deviceRow.publicKey,
		db:                 db,
		identityPrivate:    payload.IdentityPrivateKey,
		authorityPrivate:   payload.AuthorityPrivateKey,
		devicePrivate:      devicePrivate,
		rootKey:            rootKey,
		workspaceKeys:      workspaceKeys,
		clock:              clock,
		blobs:              newBlobStore(opts.DatabasePath),
	}, nil
}

// closeUnlockedSecrets wipes every secret decoded from the keybag. It is
// used only on an unlockAccountContent failure path after the keybag has
// already been opened, since Account.Lock is not reachable until a value is
// returned.
func closeUnlockedSecrets(payload keybagPlaintext, workspaceKeys map[model.ID]workspaceKeyEntry) {
	payload.IdentityPrivateKey.Close()
	payload.AuthorityPrivateKey.Close()
	for _, entry := range workspaceKeys {
		entry.Key.Close()
	}
}

type accountRow struct {
	id               model.ID
	keybagVersion    uint64
	keybagNonce      []byte
	keybagCiphertext []byte
	kdfSalt          []byte
	kdfMemoryKiB     uint32
	kdfTimeCost      uint32
	kdfParallelism   uint32
}

func loadAccountRow(ctx context.Context, db *sql.DB) (accountRow, error) {
	var row accountRow
	var idBytes []byte
	err := db.QueryRowContext(ctx,
		`SELECT id, keybag_version, keybag_nonce, keybag_ciphertext, kdf_salt, kdf_memory_kib, kdf_time_cost, kdf_parallelism FROM accounts LIMIT 1`,
	).Scan(&idBytes, &row.keybagVersion, &row.keybagNonce, &row.keybagCiphertext, &row.kdfSalt, &row.kdfMemoryKiB, &row.kdfTimeCost, &row.kdfParallelism)
	if errors.Is(err, sql.ErrNoRows) {
		return accountRow{}, ErrNoLocalAccount
	}
	if err != nil {
		return accountRow{}, fmt.Errorf("account: read account row: %w", err)
	}
	id, err := model.ParseID(idBytes)
	if err != nil {
		return accountRow{}, fmt.Errorf("account: stored account ID: %w", err)
	}
	row.id = id
	return row, nil
}

type localDeviceRow struct {
	id                 model.ID
	publicKey          []byte
	signingKeyEnvelope []byte
}

func loadLocalDeviceRow(ctx context.Context, db *sql.DB) (localDeviceRow, error) {
	var row localDeviceRow
	var idBytes []byte
	err := db.QueryRowContext(ctx,
		`SELECT id, public_key, signing_key_envelope FROM devices WHERE is_local = 1 LIMIT 1`,
	).Scan(&idBytes, &row.publicKey, &row.signingKeyEnvelope)
	if errors.Is(err, sql.ErrNoRows) {
		return localDeviceRow{}, ErrNoLocalAccount
	}
	if err != nil {
		return localDeviceRow{}, fmt.Errorf("account: read local device row: %w", err)
	}
	id, err := model.ParseID(idBytes)
	if err != nil {
		return localDeviceRow{}, fmt.Errorf("account: stored device ID: %w", err)
	}
	row.id = id
	return row, nil
}

func envelopePath(databasePath string) string {
	return databasePath + envelopeFileSuffix
}

// newBlobStore returns the BlobStore for the data directory containing
// databasePath, at the fixed layout documented in docs/architecture.md:
// <data-dir>/blobs for published content, <data-dir>/runtime for staging.
func newBlobStore(databasePath string) *store.BlobStore {
	dataDir := filepath.Dir(databasePath)
	return store.NewBlobStore(filepath.Join(dataDir, "blobs"), filepath.Join(dataDir, "runtime"))
}

// removeDatabaseFiles removes the main database file and its WAL/SHM
// sidecar files. Missing files are not an error.
func removeDatabaseFiles(databasePath string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(databasePath + suffix)
	}
}

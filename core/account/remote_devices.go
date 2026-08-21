package account

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

type RemoteDeviceRecord struct {
	ID        model.ID
	PublicKey []byte
	Active    bool
}

// UpsertRemoteDevices refreshes public verification keys from an authenticated
// server device listing. It cannot overwrite the local device row or any local
// private-key envelope.
func (a *Account) UpsertRemoteDevices(ctx context.Context, records []RemoteDeviceRecord) error {
	db, _, err := a.accountSession()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := uint64(time.Now().UnixMilli())
	for _, record := range records {
		if record.ID == a.DeviceID {
			continue
		}
		if err := record.ID.Validate(); err != nil || len(record.PublicKey) != ed25519.PublicKeySize {
			return errors.New("account: invalid remote device record")
		}
		status := 2
		if record.Active {
			status = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices(id, account_id, public_key, status, is_local,
			created_physical_ms, created_logical, created_device_id) VALUES (?, ?, ?, ?, 0, ?, 0, ?)
			ON CONFLICT(id) DO UPDATE SET public_key = excluded.public_key, status = excluded.status WHERE devices.is_local = 0`,
			record.ID.Bytes(), a.ID.Bytes(), record.PublicKey, status, now, record.ID.Bytes()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

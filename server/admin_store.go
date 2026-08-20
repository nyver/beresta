package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"
)

type UserSummary struct {
	ID            string    `json:"user_id"`
	DisplayName   string    `json:"display_name"`
	DeviceCount   int       `json:"active_devices"`
	UsedBytes     int64     `json:"used_bytes"`
	ReservedBytes int64     `json:"reserved_bytes"`
	QuotaBytes    int64     `json:"quota_bytes"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Storage) AdminListUsers(ctx context.Context) ([]UserSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.user_id, u.display_name,
		       (SELECT count(*) FROM devices d WHERE d.user_id = u.user_id AND d.revoked_at IS NULL),
		       u.used_bytes, u.reserved_bytes, u.quota_bytes, u.created_at
		FROM users u ORDER BY u.created_at, u.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []UserSummary
	for rows.Next() {
		var item UserSummary
		var created int64
		if err := rows.Scan(&item.ID, &item.DisplayName, &item.DeviceCount, &item.UsedBytes,
			&item.ReservedBytes, &item.QuotaBytes, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = time.Unix(created, 0).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Storage) AdminRevokeDevice(ctx context.Context, deviceID string, now time.Time) error {
	if err := validateID(deviceID, "device_id"); err != nil {
		return err
	}
	_, err := withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		result, err := transaction.ExecContext(ctx, `UPDATE devices SET revoked_at = ? WHERE device_id = ? AND revoked_at IS NULL`, unixNow(now), deviceID)
		if err != nil {
			return struct{}{}, err
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return struct{}{}, ErrNotFound
		}
		if _, err := transaction.ExecContext(ctx, `DELETE FROM sessions WHERE device_id = ?`, deviceID); err != nil {
			return struct{}{}, err
		}
		if _, err := transaction.ExecContext(ctx, `DELETE FROM challenges WHERE device_id = ?`, deviceID); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Storage) GarbageCollectStagingBlobs(ctx context.Context, before time.Time, dryRun bool) ([]BlobGCResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT workspace_id, blob_id, total_bytes FROM blobs
		WHERE state = 'staging' AND created_at <= ? ORDER BY workspace_id, blob_id`, unixNow(before))
	if err != nil {
		return nil, err
	}
	var result []BlobGCResult
	for rows.Next() {
		var item BlobGCResult
		if err := rows.Scan(&item.WorkspaceID, &item.BlobID, &item.Bytes); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, item)
	}
	iterationErr := rows.Err()
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if iterationErr != nil {
		return nil, iterationErr
	}
	if dryRun {
		return result, nil
	}
	for index := range result {
		s.writeMu.Lock()
		transaction, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			s.writeMu.Unlock()
			return result, err
		}
		var owner string
		var reserved int64
		err = transaction.QueryRowContext(ctx, `SELECT owner_user_id, reserved_bytes FROM blobs
			WHERE workspace_id = ? AND blob_id = ? AND state = 'staging'`, result[index].WorkspaceID, result[index].BlobID).
			Scan(&owner, &reserved)
		if errors.Is(err, sql.ErrNoRows) {
			transaction.Rollback()
			s.writeMu.Unlock()
			continue
		}
		if err == nil {
			err = os.RemoveAll(s.stagingBlobPath(result[index].WorkspaceID, result[index].BlobID))
		}
		if err == nil {
			err = os.RemoveAll(s.finalBlobPath(result[index].WorkspaceID, result[index].BlobID))
		}
		if err == nil {
			_, err = transaction.ExecContext(ctx, `DELETE FROM blobs WHERE workspace_id = ? AND blob_id = ?`, result[index].WorkspaceID, result[index].BlobID)
		}
		if err == nil {
			_, err = transaction.ExecContext(ctx, `UPDATE users SET reserved_bytes = reserved_bytes - ? WHERE user_id = ?`, reserved, owner)
		}
		if err == nil {
			err = transaction.Commit()
		} else {
			transaction.Rollback()
		}
		if err != nil {
			s.writeMu.Unlock()
			return result, err
		}
		s.writeMu.Unlock()
		result[index].Removed = true
	}
	return result, nil
}

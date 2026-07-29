package store

import (
	"context"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/model"
)

// UpsertDevice registers a device by its WireGuard public key.
//
// Re-running with the same key from the same user is a no-op refresh (the app
// re-registers on every launch), which keeps device registration idempotent
// across reinstalls that restored the keypair.
func (s *Store) UpsertDevice(ctx context.Context, userID, name, platform, publicKey, appVersion string) (model.Device, error) {
	const q = `
		INSERT INTO devices (user_id, name, platform, public_key, app_version, last_seen_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), now())
		ON CONFLICT (public_key) DO UPDATE
			SET name = EXCLUDED.name,
			    app_version = COALESCE(EXCLUDED.app_version, devices.app_version),
			    last_seen_at = now(),
			    revoked_at = NULL
			WHERE devices.user_id = EXCLUDED.user_id
		RETURNING id, user_id, name, platform, public_key, COALESCE(app_version, ''),
		          created_at, last_seen_at, revoked_at`
	var d model.Device
	err := s.pool.QueryRow(ctx, q, userID, name, platform, publicKey, appVersion).
		Scan(&d.ID, &d.UserID, &d.Name, &d.Platform, &d.PublicKey, &d.AppVersion,
			&d.CreatedAt, &d.LastSeenAt, &d.RevokedAt)
	// A key already owned by a different user fails the WHERE clause and
	// returns no rows; surface that as a conflict, not "not found".
	if err != nil && mapErr(err) == ErrNotFound {
		return model.Device{}, ErrConflict
	}
	return d, mapErr(err)
}

func (s *Store) DeviceByID(ctx context.Context, id string) (model.Device, error) {
	const q = `
		SELECT id, user_id, name, platform, public_key, COALESCE(app_version, ''),
		       created_at, last_seen_at, revoked_at
		FROM devices WHERE id = $1`
	var d model.Device
	err := s.pool.QueryRow(ctx, q, id).
		Scan(&d.ID, &d.UserID, &d.Name, &d.Platform, &d.PublicKey, &d.AppVersion,
			&d.CreatedAt, &d.LastSeenAt, &d.RevokedAt)
	return d, mapErr(err)
}

func (s *Store) DevicesByUser(ctx context.Context, userID string) ([]model.Device, error) {
	const q = `
		SELECT id, user_id, name, platform, public_key, COALESCE(app_version, ''),
		       created_at, last_seen_at, revoked_at
		FROM devices WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at`
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []model.Device
	for rows.Next() {
		var d model.Device
		if err := rows.Scan(&d.ID, &d.UserID, &d.Name, &d.Platform, &d.PublicKey,
			&d.AppVersion, &d.CreatedAt, &d.LastSeenAt, &d.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RotateDeviceKey swaps a device's public key. Callers must follow this with a
// re-provision so the edge servers learn the new key; see provision.RotateKey.
func (s *Store) RotateDeviceKey(ctx context.Context, deviceID, newPublicKey string) error {
	const q = `UPDATE devices SET public_key = $2 WHERE id = $1 AND revoked_at IS NULL`
	tag, err := s.pool.Exec(ctx, q, deviceID, newPublicKey)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RevokeDevice(ctx context.Context, deviceID string) error {
	const q = `UPDATE devices SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`
	_, err := s.pool.Exec(ctx, q, deviceID)
	return mapErr(err)
}

func (s *Store) TouchDevice(ctx context.Context, deviceID string, seen time.Time) error {
	const q = `UPDATE devices SET last_seen_at = $2 WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, deviceID, seen)
	return mapErr(err)
}

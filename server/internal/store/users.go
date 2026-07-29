package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/treykys/proxify-vpn/server/internal/model"
)

// CreateUser inserts a user. Exactly one of email/phone may be empty.
func (s *Store) CreateUser(ctx context.Context, email, phone, passwordHash string) (model.User, error) {
	const q = `
		INSERT INTO users (email, phone, password_hash)
		VALUES (NULLIF($1, ''), NULLIF($2, ''), $3)
		RETURNING id, COALESCE(email::text, ''), COALESCE(phone, ''), password_hash, status, created_at`
	var u model.User
	err := s.pool.QueryRow(ctx, q, email, phone, passwordHash).
		Scan(&u.ID, &u.Email, &u.Phone, &u.PasswordHash, &u.Status, &u.CreatedAt)
	return u, mapErr(err)
}

// UserByIdentifier looks a user up by email or phone (whichever matches).
func (s *Store) UserByIdentifier(ctx context.Context, identifier string) (model.User, error) {
	const q = `
		SELECT id, COALESCE(email::text, ''), COALESCE(phone, ''), password_hash, status, created_at
		FROM users
		WHERE status <> 'deleted' AND (email = $1::citext OR phone = $1)`
	var u model.User
	err := s.pool.QueryRow(ctx, q, identifier).
		Scan(&u.ID, &u.Email, &u.Phone, &u.PasswordHash, &u.Status, &u.CreatedAt)
	return u, mapErr(err)
}

func (s *Store) UserByID(ctx context.Context, id string) (model.User, error) {
	const q = `
		SELECT id, COALESCE(email::text, ''), COALESCE(phone, ''), password_hash, status, created_at
		FROM users WHERE id = $1 AND status <> 'deleted'`
	var u model.User
	err := s.pool.QueryRow(ctx, q, id).
		Scan(&u.ID, &u.Email, &u.Phone, &u.PasswordHash, &u.Status, &u.CreatedAt)
	return u, mapErr(err)
}

// --------------------------------------------------------------- refresh tokens

func (s *Store) CreateRefreshToken(ctx context.Context, userID string, deviceID *string, tokenHash []byte, expiresAt time.Time) (string, error) {
	const q = `
		INSERT INTO refresh_tokens (user_id, device_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4) RETURNING id`
	var id string
	err := s.pool.QueryRow(ctx, q, userID, deviceID, tokenHash, expiresAt).Scan(&id)
	return id, mapErr(err)
}

// RotateRefreshToken atomically revokes the presented token and issues a new
// one. A token presented twice (replay, or a client that retried through a
// dropped response) fails the UPDATE and returns ErrNotFound — the client falls
// back to a full login rather than two live tokens existing.
func (s *Store) RotateRefreshToken(ctx context.Context, oldHash, newHash []byte, expiresAt time.Time) (userID string, deviceID *string, err error) {
	err = s.InTx(ctx, func(tx pgx.Tx) error {
		const revoke = `
			UPDATE refresh_tokens SET revoked_at = now()
			WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
			RETURNING user_id, device_id`
		if err := tx.QueryRow(ctx, revoke, oldHash).Scan(&userID, &deviceID); err != nil {
			return err
		}
		const insert = `
			INSERT INTO refresh_tokens (user_id, device_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4)`
		_, err := tx.Exec(ctx, insert, userID, deviceID, newHash, expiresAt)
		return err
	})
	return userID, deviceID, mapErr(err)
}

func (s *Store) RevokeRefreshToken(ctx context.Context, tokenHash []byte) error {
	const q = `UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`
	_, err := s.pool.Exec(ctx, q, tokenHash)
	return mapErr(err)
}

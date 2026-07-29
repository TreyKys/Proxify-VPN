package store

import (
	"context"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/model"
)

func (s *Store) Plan(ctx context.Context, code string) (model.Plan, error) {
	const q = `
		SELECT code, name, EXTRACT(EPOCH FROM duration)::bigint, price_kobo, currency,
		       data_cap_bytes, device_limit, is_free
		FROM plans WHERE code = $1 AND active`
	var p model.Plan
	var secs int64
	err := s.pool.QueryRow(ctx, q, code).
		Scan(&p.Code, &p.Name, &secs, &p.PriceKobo, &p.Currency, &p.DataCapBytes,
			&p.DeviceLimit, &p.IsFree)
	p.Duration = time.Duration(secs) * time.Second
	return p, mapErr(err)
}

func (s *Store) Plans(ctx context.Context) ([]model.Plan, error) {
	const q = `
		SELECT code, name, EXTRACT(EPOCH FROM duration)::bigint, price_kobo, currency,
		       data_cap_bytes, device_limit, is_free
		FROM plans WHERE active ORDER BY sort_order`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []model.Plan
	for rows.Next() {
		var p model.Plan
		var secs int64
		if err := rows.Scan(&p.Code, &p.Name, &secs, &p.PriceKobo, &p.Currency,
			&p.DataCapBytes, &p.DeviceLimit, &p.IsFree); err != nil {
			return nil, err
		}
		p.Duration = time.Duration(secs) * time.Second
		out = append(out, p)
	}
	return out, rows.Err()
}

// GrantTimeBlock adds a prepaid block. Blocks stack: a purchase made while
// another block is live starts when that one ends, so a user who tops up early
// never loses the time they already paid for.
func (s *Store) GrantTimeBlock(ctx context.Context, userID, planCode, source string) (model.Subscription, error) {
	const q = `
		WITH p AS (SELECT * FROM plans WHERE code = $2),
		     base AS (
		         SELECT GREATEST(
		             now(),
		             COALESCE((SELECT max(expires_at) FROM subscriptions
		                       WHERE user_id = $1 AND revoked_at IS NULL
		                         AND expires_at > now()), now())
		         ) AS starts_at
		     )
		INSERT INTO subscriptions (user_id, plan_code, source, starts_at, expires_at, data_cap_bytes)
		SELECT $1, p.code, $3, base.starts_at, base.starts_at + p.duration, p.data_cap_bytes
		FROM p, base
		RETURNING id, user_id, plan_code, source, starts_at, expires_at, data_cap_bytes`
	var sub model.Subscription
	err := s.pool.QueryRow(ctx, q, userID, planCode, source).
		Scan(&sub.ID, &sub.UserID, &sub.PlanCode, &sub.Source, &sub.StartsAt,
			&sub.ExpiresAt, &sub.DataCapBytes)
	return sub, mapErr(err)
}

// Entitlement answers "may this user hold a tunnel right now". A user with no
// live block is inactive but not an error — the app shows the paywall.
func (s *Store) Entitlement(ctx context.Context, userID string) (model.Entitlement, error) {
	const q = `
		SELECT s.plan_code, s.expires_at, p.device_limit
		FROM subscriptions s JOIN plans p ON p.code = s.plan_code
		WHERE s.user_id = $1 AND s.revoked_at IS NULL
		  AND s.starts_at <= now() AND s.expires_at > now()
		ORDER BY p.device_limit DESC, s.expires_at DESC
		LIMIT 1`
	var e model.Entitlement
	err := s.pool.QueryRow(ctx, q, userID).Scan(&e.PlanCode, &e.ExpiresAt, &e.DeviceLimit)
	if err != nil {
		if mapErr(err) == ErrNotFound {
			return model.Entitlement{Active: false}, nil
		}
		return model.Entitlement{}, mapErr(err)
	}
	e.Active = true
	return e, nil
}

// HasEverSubscribed is used to hand the free plan out exactly once per user.
func (s *Store) HasSubscriptionOfSource(ctx context.Context, userID, source string) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM subscriptions WHERE user_id = $1 AND source = $2)`
	var ok bool
	err := s.pool.QueryRow(ctx, q, userID, source).Scan(&ok)
	return ok, mapErr(err)
}

// ExpiredUserIDs returns users whose blocks have all lapsed but who still hold
// live peer assignments — the deprovision worklist.
func (s *Store) ExpiredUserIDs(ctx context.Context, limit int) ([]string, error) {
	const q = `
		SELECT DISTINCT d.user_id
		FROM peer_assignments pa
		JOIN devices d ON d.id = pa.device_id
		WHERE pa.state IN ('pending', 'active')
		  AND NOT EXISTS (
		      SELECT 1 FROM subscriptions s
		      WHERE s.user_id = d.user_id AND s.revoked_at IS NULL
		        AND s.starts_at <= now() AND s.expires_at > now()
		  )
		LIMIT $1`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

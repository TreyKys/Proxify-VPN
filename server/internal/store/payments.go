package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// RecordWebhookEvent inserts a webhook receipt. It returns false if this
// (provider, event_id) was already seen — the caller must then do nothing.
// Paystack retries aggressively; without this a retried charge.success would
// grant the user a second time block.
func (s *Store) RecordWebhookEvent(ctx context.Context, provider, eventID, eventType string, payload []byte) (bool, error) {
	const q = `
		INSERT INTO webhook_events (provider, event_id, event_type, payload)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (provider, event_id) DO NOTHING
		RETURNING id`
	var id string
	err := s.pool.QueryRow(ctx, q, provider, eventID, eventType, payload).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, mapErr(err)
}

func (s *Store) MarkWebhookHandled(ctx context.Context, provider, eventID string) error {
	const q = `UPDATE webhook_events SET handled_at = now() WHERE provider = $1 AND event_id = $2`
	_, err := s.pool.Exec(ctx, q, provider, eventID)
	return mapErr(err)
}

type PaymentParams struct {
	UserID      string
	Provider    string
	ProviderRef string
	PlanCode    string
	AmountKobo  int64
	Currency    string
	Status      string
	Payload     []byte
}

// UpsertPayment records an initialization or a settlement. Status only moves
// forward out of 'pending', so a late-arriving initialize callback can't undo a
// success we already granted time for.
func (s *Store) UpsertPayment(ctx context.Context, p PaymentParams) (string, error) {
	if p.Provider == "" {
		p.Provider = "paystack"
	}
	if p.Currency == "" {
		p.Currency = "NGN"
	}
	if len(p.Payload) == 0 {
		p.Payload = []byte("{}")
	}
	const q = `
		INSERT INTO payments (user_id, provider, provider_ref, plan_code, amount_kobo,
		                      currency, status, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
		ON CONFLICT (provider, provider_ref) DO UPDATE SET
			status = CASE WHEN payments.status = 'pending' THEN EXCLUDED.status ELSE payments.status END,
			payload = EXCLUDED.payload,
			updated_at = now()
		RETURNING id`
	var id string
	err := s.pool.QueryRow(ctx, q, p.UserID, p.Provider, p.ProviderRef, p.PlanCode,
		p.AmountKobo, p.Currency, p.Status, p.Payload).Scan(&id)
	return id, mapErr(err)
}

type Payment struct {
	ID          string
	UserID      string
	PlanCode    string
	Status      string
	AmountKobo  int64
	ProviderRef string
}

func (s *Store) PaymentByRef(ctx context.Context, provider, ref string) (Payment, error) {
	const q = `
		SELECT id, COALESCE(user_id::text, ''), COALESCE(plan_code, ''), status, amount_kobo, provider_ref
		FROM payments WHERE provider = $1 AND provider_ref = $2`
	var p Payment
	err := s.pool.QueryRow(ctx, q, provider, ref).
		Scan(&p.ID, &p.UserID, &p.PlanCode, &p.Status, &p.AmountKobo, &p.ProviderRef)
	return p, mapErr(err)
}

// SettlePayment marks a payment successful exactly once. It returns false when
// the payment was already successful, which is the signal not to grant time
// again.
func (s *Store) SettlePayment(ctx context.Context, provider, ref string, payload []byte) (Payment, bool, error) {
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	const q = `
		UPDATE payments
		SET status = 'success', payload = $3::jsonb, updated_at = now()
		WHERE provider = $1 AND provider_ref = $2 AND status <> 'success'
		RETURNING id, COALESCE(user_id::text, ''), COALESCE(plan_code, ''), status, amount_kobo, provider_ref`
	var p Payment
	err := s.pool.QueryRow(ctx, q, provider, ref, payload).
		Scan(&p.ID, &p.UserID, &p.PlanCode, &p.Status, &p.AmountKobo, &p.ProviderRef)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := s.PaymentByRef(ctx, provider, ref)
		return existing, false, err
	}
	return p, err == nil, mapErr(err)
}

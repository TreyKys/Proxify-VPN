package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/payments/paystack"
	"github.com/treykys/proxify-vpn/server/internal/store"
)

type initializePaymentRequest struct {
	PlanCode    string `json:"plan_code"`
	CallbackURL string `json:"callback_url"`
}

func (s *Server) handleInitializePayment(w http.ResponseWriter, r *http.Request) {
	var req initializePaymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	uid := userID(r)

	plan, err := s.store.Plan(ctx, req.PlanCode)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_plan", "no such plan")
		return
	}
	if plan.IsFree || plan.PriceKobo == 0 {
		writeError(w, http.StatusBadRequest, "not_purchasable", "that plan is not purchasable")
		return
	}

	user, err := s.store.UserByID(ctx, uid)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	email := user.Email
	if email == "" {
		// Paystack requires an email. Phone-only signups get a routable-looking
		// placeholder derived from their user id; receipts go through the app.
		email = fmt.Sprintf("%s@users.proxify.ng", uid)
	}

	reference, err := newReference()
	if err != nil {
		writeDomainError(w, err)
		return
	}

	// The payment row is written before Paystack is called. If we crash between
	// the two, the worst case is an unused pending row — never a settled
	// payment we have no record of.
	if _, err := s.store.UpsertPayment(ctx, store.PaymentParams{
		UserID:      uid,
		Provider:    "paystack",
		ProviderRef: reference,
		PlanCode:    plan.Code,
		AmountKobo:  plan.PriceKobo,
		Currency:    plan.Currency,
		Status:      "pending",
	}); err != nil {
		writeDomainError(w, err)
		return
	}

	result, err := s.paystack.Initialize(ctx, paystack.InitializeParams{
		Email:       email,
		AmountKobo:  plan.PriceKobo,
		Reference:   reference,
		CallbackURL: req.CallbackURL,
		Metadata:    paystack.Metadata{UserID: uid, PlanCode: plan.Code},
		// Bank transfer and USSD matter as much as cards here — plenty of users
		// have no card at all.
		Channels: []string{"card", "bank", "ussd", "bank_transfer", "mobile_money"},
	})
	if err != nil {
		s.log.Error("paystack initialize", "err", err)
		writeError(w, http.StatusBadGateway, "payment_provider_error", "could not start the payment")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"reference":         result.Reference,
		"authorization_url": result.AuthorizationURL,
		"access_code":       result.AccessCode,
		"amount_kobo":       plan.PriceKobo,
		"plan_code":         plan.Code,
	})
}

type verifyPaymentRequest struct {
	Reference string `json:"reference"`
}

// handleVerifyPayment is the "I paid, let me in" path for when the webhook
// hasn't arrived yet. It asks Paystack directly rather than trusting the client,
// and settles through the same idempotent path the webhook uses.
func (s *Server) handleVerifyPayment(w http.ResponseWriter, r *http.Request) {
	var req verifyPaymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Reference == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "reference is required")
		return
	}

	ctx := r.Context()
	payment, err := s.store.PaymentByRef(ctx, "paystack", req.Reference)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_reference", "no such payment")
		return
	}
	if payment.UserID != userID(r) {
		// Don't confirm that someone else's reference exists.
		writeError(w, http.StatusNotFound, "unknown_reference", "no such payment")
		return
	}

	event, err := s.paystack.Verify(ctx, req.Reference)
	if err != nil {
		writeError(w, http.StatusBadGateway, "payment_provider_error", "could not verify the payment")
		return
	}
	if event.Data.Status != "success" {
		writeJSON(w, http.StatusOK, map[string]any{"status": event.Data.Status, "granted": false})
		return
	}

	granted, err := s.settleCharge(ctx, event)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	ent, err := s.store.Entitlement(ctx, userID(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "success",
		"granted":    granted,
		"active":     ent.Active,
		"expires_at": ent.ExpiresAt,
	})
}

func (s *Server) handlePaystackWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not read body")
		return
	}
	if !s.paystack.VerifySignature(body, r.Header.Get("x-paystack-signature")) {
		// Anything that fails the signature check is not from Paystack. Log it
		// as a warning without the body — an unauthenticated caller should not
		// be able to write arbitrary content into our logs.
		s.log.Warn("rejected webhook with bad signature", "bytes", len(body))
		writeError(w, http.StatusUnauthorized, "bad_signature", "signature verification failed")
		return
	}

	event, err := paystack.ParseEvent(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_event", "could not parse event")
		return
	}

	// Respond 200 quickly and do the work in the background: Paystack times
	// delivery out fast, and a timeout means a retry storm we don't need. The
	// work is idempotent, so a retry that overtakes us is harmless.
	ctx := context.WithoutCancel(r.Context())
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := s.processWebhook(ctx, event, body); err != nil {
			s.log.Error("process webhook", "event", event.Event, "ref", event.Data.Reference, "err", err)
		}
	}()

	writeJSON(w, http.StatusOK, map[string]any{"received": true})
}

func (s *Server) processWebhook(ctx context.Context, event paystack.Event, body []byte) error {
	fresh, err := s.store.RecordWebhookEvent(ctx, "paystack", event.EventID(), event.Event, body)
	if err != nil {
		return err
	}
	if !fresh {
		// Already processed this delivery.
		return nil
	}

	switch event.Event {
	case "charge.success":
		if _, err := s.settleCharge(ctx, event); err != nil {
			return err
		}
	default:
		// Recorded and ignored: we only sell prepaid blocks, so refunds,
		// disputes and subscription events have no state to change in v1.
		s.log.Info("ignoring webhook event", "event", event.Event)
	}
	return s.store.MarkWebhookHandled(ctx, "paystack", event.EventID())
}

// settleCharge is the single place a successful payment turns into time. Both
// the webhook and the client-triggered verify funnel through it, and it grants
// exactly once per reference.
func (s *Server) settleCharge(ctx context.Context, event paystack.Event) (bool, error) {
	ref := event.Data.Reference
	if ref == "" {
		return false, fmt.Errorf("charge.success without a reference")
	}

	payment, err := s.store.PaymentByRef(ctx, "paystack", ref)
	if err != nil {
		// A reference we never initialized. Metadata may still identify the
		// user (e.g. a payment link), but we refuse to grant time from a
		// payment we cannot tie to a plan we priced.
		s.log.Warn("charge for unknown reference", "ref", ref)
		return false, nil
	}

	// Underpayment check. Paystack amounts are in kobo; a mismatch means the
	// charge doesn't correspond to the plan we recorded, so it grants nothing.
	if event.Data.Amount < payment.AmountKobo {
		s.log.Warn("underpaid charge ignored",
			"ref", ref, "paid_kobo", event.Data.Amount, "expected_kobo", payment.AmountKobo)
		return false, nil
	}

	raw, _ := json.Marshal(event)
	settled, firstTime, err := s.store.SettlePayment(ctx, "paystack", ref, raw)
	if err != nil {
		return false, err
	}
	if !firstTime {
		return false, nil
	}

	userID := settled.UserID
	if userID == "" {
		userID = event.Data.Metadata.UserID
	}
	planCode := settled.PlanCode
	if planCode == "" {
		planCode = event.Data.Metadata.PlanCode
	}
	if userID == "" || planCode == "" {
		return false, fmt.Errorf("settled payment %s has no user/plan to grant", ref)
	}

	sub, err := s.store.GrantTimeBlock(ctx, userID, planCode, "paystack")
	if err != nil {
		return false, fmt.Errorf("grant time block: %w", err)
	}
	s.log.Info("granted time block",
		"user", userID, "plan", planCode, "expires_at", sub.ExpiresAt, "ref", ref)
	return true, nil
}

func newReference() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "pxf_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

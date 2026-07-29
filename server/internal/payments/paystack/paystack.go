// Package paystack implements the Paystack integration: initializing a
// transaction and verifying the webhook that settles it.
//
// Money flow, deliberately: the webhook is the only thing that grants time. The
// app's "payment succeeded" callback is treated as a hint to refresh, never as
// proof — a client-supplied success is not a payment.
package paystack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const apiBase = "https://api.paystack.co"

type Client struct {
	secretKey  string
	webhookKey string
	hc         *http.Client
}

func New(secretKey, webhookKey string) *Client {
	if webhookKey == "" {
		webhookKey = secretKey
	}
	return &Client{
		secretKey:  secretKey,
		webhookKey: webhookKey,
		hc:         &http.Client{Timeout: 15 * time.Second},
	}
}

// VerifySignature checks the x-paystack-signature header (HMAC-SHA512 of the
// raw body, keyed with the secret key). Compared in constant time, and against
// the exact bytes we received — never a re-serialized version.
func (c *Client) VerifySignature(body []byte, signature string) bool {
	mac := hmac.New(sha512.New, []byte(c.webhookKey))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// Event is the subset of a Paystack webhook we act on.
type Event struct {
	Event string `json:"event"`
	Data  struct {
		ID        int64  `json:"id"`
		Reference string `json:"reference"`
		Status    string `json:"status"`
		Amount    int64  `json:"amount"` // kobo
		Currency  string `json:"currency"`
		PaidAt    string `json:"paid_at"`
		Customer  struct {
			Email string `json:"email"`
		} `json:"customer"`
		Metadata Metadata `json:"metadata"`
	} `json:"data"`
}

// Metadata is what we attach at initialize time and read back on the webhook.
// It is the link between a Paystack reference and our user/plan; we never trust
// amounts or plan codes that arrive without a matching payment row.
type Metadata struct {
	UserID   string `json:"user_id"`
	PlanCode string `json:"plan_code"`
}

// UnmarshalJSON tolerates Paystack sending metadata as a JSON string (it does
// this when metadata was submitted as a string field) as well as an object.
func (m *Metadata) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" || string(b) == `""` {
		return nil
	}
	type alias Metadata
	var direct alias
	if err := json.Unmarshal(b, &direct); err == nil {
		*m = Metadata(direct)
		return nil
	}
	var asString string
	if err := json.Unmarshal(b, &asString); err != nil {
		return nil // unknown shape: ignore rather than reject the whole event
	}
	var nested alias
	if err := json.Unmarshal([]byte(asString), &nested); err != nil {
		return nil
	}
	*m = Metadata(nested)
	return nil
}

// EventID returns a stable idempotency key for a webhook delivery. Paystack
// does not send a delivery ID, so the transaction id + event type is the
// strongest key available.
func (e Event) EventID() string {
	if e.Data.ID != 0 {
		return fmt.Sprintf("%s:%d", e.Event, e.Data.ID)
	}
	return fmt.Sprintf("%s:%s", e.Event, e.Data.Reference)
}

func ParseEvent(body []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		return Event{}, fmt.Errorf("paystack: decode event: %w", err)
	}
	if e.Event == "" {
		return Event{}, fmt.Errorf("paystack: event missing type")
	}
	return e, nil
}

// InitializeParams describes a checkout to open.
type InitializeParams struct {
	Email      string
	AmountKobo int64
	Reference  string
	Metadata   Metadata
	// CallbackURL is where Paystack sends the browser afterwards. The app
	// treats arrival there as "go re-check my subscription", nothing more.
	CallbackURL string
	Channels    []string
}

type InitializeResult struct {
	AuthorizationURL string `json:"authorization_url"`
	AccessCode       string `json:"access_code"`
	Reference        string `json:"reference"`
}

func (c *Client) Initialize(ctx context.Context, p InitializeParams) (InitializeResult, error) {
	payload := map[string]any{
		"email":     p.Email,
		"amount":    p.AmountKobo,
		"reference": p.Reference,
		"metadata":  p.Metadata,
		"currency":  "NGN",
	}
	if p.CallbackURL != "" {
		payload["callback_url"] = p.CallbackURL
	}
	if len(p.Channels) > 0 {
		payload["channels"] = p.Channels
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return InitializeResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/transaction/initialize", bytes.NewReader(body))
	if err != nil {
		return InitializeResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return InitializeResult{}, fmt.Errorf("paystack: initialize: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return InitializeResult{}, err
	}
	var envelope struct {
		Status  bool             `json:"status"`
		Message string           `json:"message"`
		Data    InitializeResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return InitializeResult{}, fmt.Errorf("paystack: decode initialize response (%s): %w", resp.Status, err)
	}
	if !envelope.Status {
		return InitializeResult{}, fmt.Errorf("paystack: initialize rejected: %s", envelope.Message)
	}
	return envelope.Data, nil
}

// Verify re-checks a transaction directly with Paystack. Used as a fallback
// when a user returns to the app before the webhook lands, so a paying user is
// never stuck staring at a paywall waiting on webhook delivery.
func (c *Client) Verify(ctx context.Context, reference string) (Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/transaction/verify/"+reference, nil)
	if err != nil {
		return Event{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)

	resp, err := c.hc.Do(req)
	if err != nil {
		return Event{}, fmt.Errorf("paystack: verify: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Event{}, err
	}
	var envelope struct {
		Status bool            `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Event{}, fmt.Errorf("paystack: decode verify response (%s): %w", resp.Status, err)
	}
	if !envelope.Status {
		return Event{}, fmt.Errorf("paystack: verify failed for %s", reference)
	}
	var e Event
	e.Event = "charge.success"
	if err := json.Unmarshal(envelope.Data, &e.Data); err != nil {
		return Event{}, err
	}
	return e, nil
}

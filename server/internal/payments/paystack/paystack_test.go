package paystack

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	const secret = "sk_test_secret"
	c := New(secret, "")
	body := []byte(`{"event":"charge.success","data":{"reference":"pxf_1"}}`)

	if !c.VerifySignature(body, sign(secret, body)) {
		t.Error("valid signature rejected")
	}
	if c.VerifySignature(body, sign("wrong-secret", body)) {
		t.Error("signature from the wrong secret accepted")
	}
	if c.VerifySignature(append(body, ' '), sign(secret, body)) {
		t.Error("signature accepted for modified body")
	}
	if c.VerifySignature(body, "") {
		t.Error("empty signature accepted")
	}
}

func TestParseEventAndMetadata(t *testing.T) {
	body := []byte(`{
		"event": "charge.success",
		"data": {
			"id": 992001,
			"reference": "pxf_abc",
			"status": "success",
			"amount": 200000,
			"metadata": {"user_id": "u-1", "plan_code": "monthly"}
		}}`)

	e, err := ParseEvent(body)
	if err != nil {
		t.Fatal(err)
	}
	if e.Data.Metadata.UserID != "u-1" || e.Data.Metadata.PlanCode != "monthly" {
		t.Errorf("metadata = %+v", e.Data.Metadata)
	}
	if e.EventID() != "charge.success:992001" {
		t.Errorf("EventID = %q", e.EventID())
	}
}

// Paystack sometimes echoes metadata back as a JSON-encoded string rather than
// an object. Both shapes must yield the same grant.
func TestMetadataAsEncodedString(t *testing.T) {
	body := []byte(`{
		"event": "charge.success",
		"data": {
			"id": 1,
			"reference": "pxf_abc",
			"metadata": "{\"user_id\":\"u-2\",\"plan_code\":\"weekly\"}"
		}}`)

	e, err := ParseEvent(body)
	if err != nil {
		t.Fatal(err)
	}
	if e.Data.Metadata.UserID != "u-2" || e.Data.Metadata.PlanCode != "weekly" {
		t.Errorf("metadata = %+v", e.Data.Metadata)
	}
}

func TestParseEventRejectsGarbage(t *testing.T) {
	if _, err := ParseEvent([]byte(`not json`)); err == nil {
		t.Error("accepted non-JSON body")
	}
	if _, err := ParseEvent([]byte(`{"data":{}}`)); err == nil {
		t.Error("accepted an event with no type")
	}
}

// EventID falls back to the reference when Paystack omits the transaction id,
// so replay protection still has a key to work with.
func TestEventIDFallsBackToReference(t *testing.T) {
	e, err := ParseEvent([]byte(`{"event":"charge.success","data":{"reference":"pxf_x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.EventID() != "charge.success:pxf_x" {
		t.Errorf("EventID = %q", e.EventID())
	}
}

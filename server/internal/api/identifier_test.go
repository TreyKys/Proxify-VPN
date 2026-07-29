package api

import "testing"

// Every Nigerian number below is the same number typed the way real users type
// it. They must all resolve to one account.
func TestNormalizePhone(t *testing.T) {
	same := []string{
		"08031234567",
		"8031234567",
		"+2348031234567",
		"2348031234567",
		"0803 123 4567",
		"+234 803 123 4567",
		"(0803) 123-4567",
	}
	const want = "+2348031234567"
	for _, in := range same {
		got, err := normalizePhone(in)
		if err != nil {
			t.Errorf("normalizePhone(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizePhone(%q) = %q, want %q", in, got, want)
		}
	}

	for _, bad := range []string{"", "123", "abcdefghij", "0803123456789012345"} {
		if _, err := normalizePhone(bad); err == nil {
			t.Errorf("normalizePhone(%q) should have failed", bad)
		}
	}
}

func TestSplitIdentifier(t *testing.T) {
	email, phone, err := splitIdentifier("  Trey@Example.COM ")
	if err != nil {
		t.Fatal(err)
	}
	if email != "trey@example.com" || phone != "" {
		t.Errorf("email = %q, phone = %q", email, phone)
	}

	email, phone, err = splitIdentifier("08031234567")
	if err != nil {
		t.Fatal(err)
	}
	if phone != "+2348031234567" || email != "" {
		t.Errorf("email = %q, phone = %q", email, phone)
	}

	for _, bad := range []string{"", "@", "no-at-sign", "trey@", "@example.com"} {
		if _, _, err := splitIdentifier(bad); err == nil {
			t.Errorf("splitIdentifier(%q) should have failed", bad)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	rl := newRateLimiter(3, 0)
	rl.window = 1 << 40 // effectively never rolls over during the test

	for i := range 3 {
		if !rl.Allow("user@example.com") {
			t.Fatalf("request %d blocked before the limit", i+1)
		}
	}
	if rl.Allow("user@example.com") {
		t.Error("limiter allowed a request past the limit")
	}
	// Limits are per identifier: one user being throttled must not lock out
	// everyone else behind the same carrier NAT.
	if !rl.Allow("other@example.com") {
		t.Error("a different identifier was throttled")
	}
}

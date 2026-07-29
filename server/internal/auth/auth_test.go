package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "correct-horse-battery") {
		t.Error("correct password rejected")
	}
	if CheckPassword(hash, "wrong-password") {
		t.Error("wrong password accepted")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Error("accepted a password below the minimum length")
	}
	// bcrypt truncates past 72 bytes; accepting one would silently ignore the
	// tail of the password.
	if _, err := HashPassword(strings.Repeat("a", 100)); err == nil {
		t.Error("accepted an over-length password")
	}
}

func TestAccessTokenRoundTrip(t *testing.T) {
	s := NewSigner([]byte("test-secret-that-is-long-enough!!"), time.Hour)

	token, exp, err := s.Issue("user-1", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if !exp.After(time.Now()) {
		t.Error("expiry is not in the future")
	}

	claims, err := s.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user-1" || claims.DeviceID != "device-1" {
		t.Errorf("claims = %+v", claims)
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	s := NewSigner([]byte("test-secret-that-is-long-enough!!"), time.Hour)
	token, _, err := s.Issue("user-1", "")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")

	cases := map[string]string{
		"swapped signature": parts[0] + "." + parts[1] + ".AAAA",
		"missing signature": parts[0] + "." + parts[1],
		// The classic JWT attack: claim an algorithm we don't use and hope the
		// verifier trusts the header.
		"alg none": "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." + parts[1] + ".",
		"empty":    "",
	}
	for name, bad := range cases {
		if _, err := s.Verify(bad); err == nil {
			t.Errorf("Verify accepted %s", name)
		}
	}

	// A token signed with a different secret must not verify.
	other := NewSigner([]byte("a-completely-different-secret!!!!"), time.Hour)
	if _, err := other.Verify(token); err == nil {
		t.Error("token verified under the wrong secret")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	s := NewSigner([]byte("test-secret-that-is-long-enough!!"), time.Minute)
	token, _, err := s.Issue("user-1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return time.Now().Add(2 * time.Minute) })
	if _, err := s.Verify(token); err != ErrExpiredToken {
		t.Errorf("err = %v, want ErrExpiredToken", err)
	}
}

func TestRefreshTokenHashing(t *testing.T) {
	token, hash, err := NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || len(hash) != 32 {
		t.Fatalf("token = %q, hash len = %d", token, len(hash))
	}
	if string(HashRefreshToken(token)) != string(hash) {
		t.Error("hashing is not deterministic")
	}
	if strings.Contains(string(hash), token) {
		t.Error("hash contains the raw token")
	}
}

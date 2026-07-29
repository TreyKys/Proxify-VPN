// Package auth handles password hashing, access tokens and refresh tokens.
//
// Access tokens are self-signed JWT-shaped bearer tokens (HMAC-SHA256) so the
// API stays stateless on the hot path. Refresh tokens are opaque random strings
// stored hashed, so a database leak doesn't hand out live sessions.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidToken = errors.New("auth: invalid token")
	ErrExpiredToken = errors.New("auth: token expired")
)

// bcryptCost 12 is ~250ms on the small VPS class we run on. Login is not a hot
// path; brute-force resistance matters more than latency here.
const bcryptCost = 12

func HashPassword(plain string) (string, error) {
	if len(plain) < 8 {
		return "", errors.New("auth: password must be at least 8 characters")
	}
	// bcrypt silently truncates at 72 bytes; reject rather than accept a
	// password whose tail is ignored.
	if len(plain) > 72 {
		return "", errors.New("auth: password must be at most 72 bytes")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	return string(h), err
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ---------------------------------------------------------------- access tokens

type Claims struct {
	Subject  string `json:"sub"`
	DeviceID string `json:"did,omitempty"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
}

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type Signer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewSigner(secret []byte, ttl time.Duration) *Signer {
	return &Signer{secret: secret, ttl: ttl, now: time.Now}
}

// SetClock is a test seam.
func (s *Signer) SetClock(now func() time.Time) { s.now = now }

func (s *Signer) Issue(userID, deviceID string) (string, time.Time, error) {
	now := s.now()
	exp := now.Add(s.ttl)
	claims := Claims{Subject: userID, DeviceID: deviceID, IssuedAt: now.Unix(), Expires: exp.Unix()}

	h, err := json.Marshal(header{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", time.Time{}, err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}
	signing := b64(h) + "." + b64(c)
	return signing + "." + b64(s.sign(signing)), exp, nil
}

func (s *Signer) Verify(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	if !hmac.Equal(sig, s.sign(parts[0]+"."+parts[1])) {
		return Claims{}, ErrInvalidToken
	}

	rawHeader, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var hdr header
	if err := json.Unmarshal(rawHeader, &hdr); err != nil || hdr.Alg != "HS256" {
		// Reject anything that isn't the algorithm we issue, so a token
		// claiming alg:none never reaches the claim parser.
		return Claims{}, ErrInvalidToken
	}

	rawClaims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var claims Claims
	if err := json.Unmarshal(rawClaims, &claims); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if claims.Subject == "" {
		return Claims{}, ErrInvalidToken
	}
	if s.now().Unix() >= claims.Expires {
		return Claims{}, ErrExpiredToken
	}
	return claims, nil
}

func (s *Signer) sign(msg string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(msg))
	return mac.Sum(nil)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// --------------------------------------------------------------- refresh tokens

// NewRefreshToken returns the token to hand to the client and the hash to store.
func NewRefreshToken() (token string, hash []byte, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("auth: generate refresh token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashRefreshToken(token), nil
}

func HashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ConstantTimeEqual is used for shared-secret comparisons (edge agent tokens).
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

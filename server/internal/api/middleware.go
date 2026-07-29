package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/auth"
)

type ctxKey string

const (
	ctxUserID   ctxKey = "user_id"
	ctxDeviceID ctxKey = "device_id"
)

func userID(r *http.Request) string {
	v, _ := r.Context().Value(ctxUserID).(string)
	return v
}

// authenticated wraps a handler with bearer-token auth.
func (s *Server) authenticated(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		claims, err := s.signer.Verify(token)
		if err != nil {
			code := "unauthorized"
			if errors.Is(err, auth.ErrExpiredToken) {
				// Distinguished so the app refreshes instead of logging the
				// user out — a forced re-login on a flaky network is exactly
				// the failure mode we're supposed to be better than.
				code = "token_expired"
			}
			writeError(w, http.StatusUnauthorized, code, "invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserID, claims.Subject)
		ctx = context.WithValue(ctx, ctxDeviceID, claims.DeviceID)
		next(w, r.WithContext(ctx))
	})
}

func (s *Server) adminOnly(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.adminToken == "" {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		if !auth.ConstantTimeEqual(bearerToken(r), s.adminToken) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "admin token required")
			return
		}
		next(w, r)
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

// requestLog logs method, path, status and duration — and deliberately not the
// client IP, user agent, or any request body. See docs/logging-policy.md: the
// no-logs claim has to be true in the code, not just in the privacy policy.
func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "path", r.URL.Path, "panic", rec)
				writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wrote {
		return
	}
	s.wrote = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// ---------------------------------------------------------------- rate limiting

// rateLimiter is a fixed-window counter keyed by identifier. It is intentionally
// in-process and approximate: it exists to blunt credential stuffing on a single
// box, not to be a distributed quota system.
//
// The key is the submitted identifier (email/phone), not the IP address — most
// Nigerian mobile users share carrier-grade NAT, so IP-keyed limits would lock
// out whole networks while barely inconveniencing an attacker.
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*window
	now    func() time.Time
}

type window struct {
	count int
	start time.Time
}

func newRateLimiter(limit int, per time.Duration) *rateLimiter {
	return &rateLimiter{
		limit:  limit,
		window: per,
		hits:   map[string]*window{},
		now:    time.Now,
	}
}

// Allow reports whether the key may proceed, and evicts stale windows as it goes
// so the map cannot grow without bound.
func (r *rateLimiter) Allow(key string) bool {
	if key == "" {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if len(r.hits) > 10000 {
		for k, w := range r.hits {
			if now.Sub(w.start) > r.window {
				delete(r.hits, k)
			}
		}
	}

	w, ok := r.hits[key]
	if !ok || now.Sub(w.start) > r.window {
		r.hits[key] = &window{count: 1, start: now}
		return true
	}
	w.count++
	return w.count <= r.limit
}

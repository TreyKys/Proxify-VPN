package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/auth"
	"github.com/treykys/proxify-vpn/server/internal/store"
)

type credentials struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

type tokenResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	UserID       string    `json:"user_id"`
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decodeJSON(w, r, &req) {
		return
	}
	email, phone, err := splitIdentifier(req.Identifier)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_identifier", err.Error())
		return
	}
	if !s.signupLimiter.Allow(email + phone) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}

	ctx := r.Context()
	user, err := s.store.CreateUser(ctx, email, phone, hash)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "account_exists", "an account with that identifier already exists")
			return
		}
		s.log.Error("create user", "err", err)
		writeDomainError(w, err)
		return
	}

	// The free data-capped tier is the acquisition on-ramp (brief §11); a new
	// account can connect immediately rather than hitting a paywall first.
	if s.cfg.FreePlanEnabled {
		if _, err := s.store.GrantTimeBlock(ctx, user.ID, "free", "free"); err != nil {
			s.log.Error("grant free plan", "user", user.ID, "err", err)
		}
	}

	s.issueTokens(w, r, user.ID, nil)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decodeJSON(w, r, &req) {
		return
	}
	email, phone, err := splitIdentifier(req.Identifier)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_identifier", err.Error())
		return
	}
	identifier := email
	if identifier == "" {
		identifier = phone
	}
	if !s.loginLimiter.Allow(identifier) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
		return
	}

	user, err := s.store.UserByIdentifier(r.Context(), identifier)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Same response as a wrong password: don't confirm which
			// identifiers exist.
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "wrong identifier or password")
			return
		}
		writeDomainError(w, err)
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "wrong identifier or password")
		return
	}
	if user.Status != "active" {
		writeError(w, http.StatusForbidden, "account_suspended", "this account is suspended")
		return
	}

	s.issueTokens(w, r, user.ID, nil)
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}

	newToken, newHash, err := auth.NewRefreshToken()
	if err != nil {
		writeDomainError(w, err)
		return
	}
	expires := time.Now().Add(s.cfg.RefreshTokenTTL)
	uid, deviceID, err := s.store.RotateRefreshToken(r.Context(),
		auth.HashRefreshToken(req.RefreshToken), newHash, expires)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_refresh", "refresh token is invalid or expired")
		return
	}

	device := ""
	if deviceID != nil {
		device = *deviceID
	}
	access, accessExp, err := s.signer.Issue(uid, device)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  access,
		RefreshToken: newToken,
		ExpiresAt:    accessExp,
		UserID:       uid,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RefreshToken != "" {
		if err := s.store.RevokeRefreshToken(r.Context(), auth.HashRefreshToken(req.RefreshToken)); err != nil {
			s.log.Error("revoke refresh token", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) issueTokens(w http.ResponseWriter, r *http.Request, uid string, deviceID *string) {
	access, accessExp, err := s.signer.Issue(uid, derefOr(deviceID, ""))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	refresh, refreshHash, err := auth.NewRefreshToken()
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if _, err := s.store.CreateRefreshToken(r.Context(), uid, deviceID, refreshHash,
		time.Now().Add(s.cfg.RefreshTokenTTL)); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    accessExp,
		UserID:       uid,
	})
}

func derefOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := userID(r)

	user, err := s.store.UserByID(ctx, uid)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	ent, err := s.store.Entitlement(ctx, uid)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	devices, err := s.store.DevicesByUser(ctx, uid)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	type deviceView struct {
		ID        string     `json:"id"`
		Name      string     `json:"name"`
		Platform  string     `json:"platform"`
		CreatedAt time.Time  `json:"created_at"`
		LastSeen  *time.Time `json:"last_seen_at,omitempty"`
	}
	views := make([]deviceView, 0, len(devices))
	for _, d := range devices {
		views = append(views, deviceView{
			ID: d.ID, Name: d.Name, Platform: d.Platform,
			CreatedAt: d.CreatedAt, LastSeen: d.LastSeenAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":         user.ID,
			"email":      user.Email,
			"phone":      user.Phone,
			"created_at": user.CreatedAt,
		},
		"subscription": map[string]any{
			"active":       ent.Active,
			"plan_code":    ent.PlanCode,
			"expires_at":   ent.ExpiresAt,
			"device_limit": ent.DeviceLimit,
		},
		"devices": views,
	})
}

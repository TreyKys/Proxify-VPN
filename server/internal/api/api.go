// Package api is the control plane's HTTP surface.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/auth"
	"github.com/treykys/proxify-vpn/server/internal/config"
	"github.com/treykys/proxify-vpn/server/internal/payments/paystack"
	"github.com/treykys/proxify-vpn/server/internal/provision"
	"github.com/treykys/proxify-vpn/server/internal/store"
)

type Server struct {
	cfg        config.Config
	store      *store.Store
	provision  *provision.Service
	reconciler *provision.Reconciler
	signer     *auth.Signer
	paystack   *paystack.Client
	log        *slog.Logger

	// adminToken guards the operator endpoints. Empty disables them entirely,
	// which is the right default for any deployment that hasn't set one.
	adminToken string

	loginLimiter  *rateLimiter
	signupLimiter *rateLimiter
}

type Deps struct {
	Config     config.Config
	Store      *store.Store
	Provision  *provision.Service
	Reconciler *provision.Reconciler
	Signer     *auth.Signer
	Paystack   *paystack.Client
	Logger     *slog.Logger
	AdminToken string
}

func NewServer(d Deps) *Server {
	return &Server{
		cfg:        d.Config,
		store:      d.Store,
		provision:  d.Provision,
		reconciler: d.Reconciler,
		signer:     d.Signer,
		paystack:   d.Paystack,
		log:        d.Logger,
		adminToken: d.AdminToken,
		// Credential endpoints are the ones worth throttling: everything else
		// is already behind a bearer token.
		loginLimiter:  newRateLimiter(10, time.Minute),
		signupLimiter: newRateLimiter(5, 10*time.Minute),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)

	// Auth
	mux.HandleFunc("POST /v1/auth/signup", s.handleSignup)
	mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /v1/auth/logout", s.handleLogout)

	// Account
	mux.Handle("GET /v1/me", s.authenticated(s.handleMe))

	// Devices
	mux.Handle("POST /v1/devices", s.authenticated(s.handleRegisterDevice))
	mux.Handle("GET /v1/devices", s.authenticated(s.handleListDevices))
	mux.Handle("DELETE /v1/devices/{id}", s.authenticated(s.handleDeleteDevice))
	mux.Handle("POST /v1/devices/{id}/rotate-key", s.authenticated(s.handleRotateKey))

	// Catalogue
	mux.HandleFunc("GET /v1/servers", s.handleListServers)
	mux.HandleFunc("GET /v1/plans", s.handleListPlans)

	// The §7 endpoints
	mux.Handle("POST /v1/tunnel/provision", s.authenticated(s.handleProvision))
	mux.Handle("POST /v1/tunnel/release", s.authenticated(s.handleRelease))
	mux.Handle("GET /v1/tunnel/status", s.authenticated(s.handleTunnelStatus))

	// Payments
	mux.Handle("POST /v1/payments/initialize", s.authenticated(s.handleInitializePayment))
	mux.Handle("POST /v1/payments/verify", s.authenticated(s.handleVerifyPayment))
	mux.HandleFunc("POST /v1/webhooks/paystack", s.handlePaystackWebhook)

	// Operator endpoints
	mux.Handle("POST /v1/admin/servers", s.adminOnly(s.handleUpsertServer))
	mux.Handle("POST /v1/admin/servers/{code}/status", s.adminOnly(s.handleServerStatus))
	mux.Handle("POST /v1/admin/servers/{code}/resync", s.adminOnly(s.handleResync))
	mux.Handle("POST /v1/admin/reconcile", s.adminOnly(s.handleReconcileNow))
	mux.Handle("POST /v1/admin/grant", s.adminOnly(s.handleGrant))

	return s.recoverPanics(s.requestLog(mux))
}

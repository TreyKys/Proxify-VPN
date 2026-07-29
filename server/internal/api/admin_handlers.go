package api

import (
	"net/http"

	"github.com/treykys/proxify-vpn/server/internal/model"
	"github.com/treykys/proxify-vpn/server/internal/store"
	"github.com/treykys/proxify-vpn/server/internal/wgkey"
)

type upsertServerRequest struct {
	Code          string         `json:"code"`
	DisplayName   string         `json:"display_name"`
	CountryCode   string         `json:"country_code"`
	Region        string         `json:"region"`
	EndpointHost  string         `json:"endpoint_host"`
	EndpointPort  int            `json:"endpoint_port"`
	PublicKey     string         `json:"public_key"`
	Obfuscation   map[string]any `json:"obfuscation"`
	TunnelSubnet  string         `json:"tunnel_subnet"`
	AgentURL      string         `json:"agent_url"`
	AgentToken    string         `json:"agent_token"`
	Status        string         `json:"status"`
	CapacityPeers int            `json:"capacity_peers"`
	Priority      int            `json:"priority"`
}

// handleUpsertServer registers an edge box. This is what edge/scripts/register.sh
// calls at the end of provisioning a machine.
func (s *Server) handleUpsertServer(w http.ResponseWriter, r *http.Request) {
	var req upsertServerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	key, err := wgkey.Validate(req.PublicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_public_key", "public_key must be a base64 WireGuard key")
		return
	}
	if req.Code == "" || req.EndpointHost == "" || req.TunnelSubnet == "" || req.AgentURL == "" {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"code, endpoint_host, tunnel_subnet and agent_url are required")
		return
	}
	if req.EndpointPort == 0 {
		req.EndpointPort = 51820
	}
	if req.CapacityPeers == 0 {
		req.CapacityPeers = 500
	}
	if req.Priority == 0 {
		req.Priority = 100
	}
	if req.Status == "" {
		// New boxes land in 'draining': registered and reachable, but not
		// handed to users until an operator has verified them.
		req.Status = string(model.ServerDraining)
	}

	srv, err := s.store.UpsertServer(r.Context(), store.UpsertServerParams{
		Code:          req.Code,
		DisplayName:   orDefault(req.DisplayName, req.Code),
		CountryCode:   req.CountryCode,
		Region:        req.Region,
		EndpointHost:  req.EndpointHost,
		EndpointPort:  req.EndpointPort,
		PublicKey:     key,
		Obfuscation:   req.Obfuscation,
		TunnelSubnet:  req.TunnelSubnet,
		AgentURL:      req.AgentURL,
		AgentToken:    req.AgentToken,
		Status:        req.Status,
		CapacityPeers: req.CapacityPeers,
		Priority:      req.Priority,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": srv.ID, "code": srv.Code, "status": srv.Status,
	})
}

type serverStatusRequest struct {
	Status string `json:"status"`
}

func (s *Server) handleServerStatus(w http.ResponseWriter, r *http.Request) {
	var req serverStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	switch model.ServerStatus(req.Status) {
	case model.ServerActive, model.ServerDraining, model.ServerDown, model.ServerMaintenance:
	default:
		writeError(w, http.StatusBadRequest, "invalid_status", "unknown status")
		return
	}
	if err := s.store.SetServerStatus(r.Context(), r.PathValue("code"), model.ServerStatus(req.Status)); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": r.PathValue("code"), "status": req.Status})
}

// handleResync force-pushes the full desired peer set to a box. The repair tool
// for "I rebuilt that server and forgot to restore /etc/wireguard".
func (s *Server) handleResync(w http.ResponseWriter, r *http.Request) {
	srv, err := s.store.ServerByCode(r.Context(), r.PathValue("code"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if err := s.reconciler.Resync(r.Context(), srv); err != nil {
		writeError(w, http.StatusBadGateway, "resync_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resynced": srv.Code})
}

func (s *Server) handleReconcileNow(w http.ResponseWriter, r *http.Request) {
	if err := s.reconciler.Once(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "reconcile_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type grantRequest struct {
	Identifier string `json:"identifier"`
	PlanCode   string `json:"plan_code"`
}

// handleGrant hands a user a time block without a payment: support fixes,
// beta testers, influencer comps.
func (s *Server) handleGrant(w http.ResponseWriter, r *http.Request) {
	var req grantRequest
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

	user, err := s.store.UserByIdentifier(r.Context(), identifier)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sub, err := s.store.GrantTimeBlock(r.Context(), user.ID, req.PlanCode, "manual")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.log.Info("manual grant", "user", user.ID, "plan", req.PlanCode, "expires_at", sub.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": user.ID, "plan_code": sub.PlanCode, "expires_at": sub.ExpiresAt,
	})
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

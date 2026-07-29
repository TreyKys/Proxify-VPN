package api

import (
	"net/http"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/model"
	"github.com/treykys/proxify-vpn/server/internal/provision"
)

type provisionRequest struct {
	DeviceID   string `json:"device_id"`
	ServerCode string `json:"server_code"`
}

// handleProvision is the endpoint the app calls before every connect. It is
// idempotent, so calling it on every launch (which the app does) costs one
// database round trip once the peer is live.
func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	var req provisionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "device_id is required")
		return
	}

	cfg, err := s.provision.Provision(r.Context(), provision.Request{
		UserID:     userID(r),
		DeviceID:   req.DeviceID,
		ServerCode: req.ServerCode,
		// Cloudflare sits in front of the API (brief §8), so this is present in
		// production and empty locally. It only breaks ties in selection, and
		// it is used in-request and never stored.
		ClientCountry: r.Header.Get("CF-IPCountry"),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	if err := s.store.TouchDevice(r.Context(), req.DeviceID, time.Now()); err != nil {
		s.log.Warn("touch device", "device", req.DeviceID, "err", err)
	}
	writeJSON(w, http.StatusOK, cfg)
}

type releaseRequest struct {
	DeviceID string `json:"device_id"`
}

// handleRelease tears a device's peers down on request (user pressed
// disconnect-and-forget, or is switching accounts).
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	var req releaseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	device, err := s.store.DeviceByID(r.Context(), req.DeviceID)
	if err != nil || device.UserID != userID(r) {
		writeError(w, http.StatusNotFound, "not_found", "device not found")
		return
	}
	n, err := s.provision.DeprovisionDevice(r.Context(), req.DeviceID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoking": n})
}

// handleTunnelStatus lets the app see whether its peer is actually installed
// on the edge yet. The app polls this after a 503 from provision rather than
// blindly re-dialing a tunnel that cannot come up.
func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "device_id is required")
		return
	}
	device, err := s.store.DeviceByID(r.Context(), deviceID)
	if err != nil || device.UserID != userID(r) {
		writeError(w, http.StatusNotFound, "not_found", "device not found")
		return
	}

	assignments, err := s.store.AssignmentsForDevice(r.Context(), deviceID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	type view struct {
		ServerCode string `json:"server_code"`
		State      string `json:"state"`
		Ready      bool   `json:"ready"`
		TunnelIP   string `json:"tunnel_ip"`
	}
	out := make([]view, 0, len(assignments))
	for _, a := range assignments {
		srv, err := s.store.ServerByID(r.Context(), a.ServerID)
		code := ""
		if err == nil {
			code = srv.Code
		}
		out = append(out, view{
			ServerCode: code,
			State:      string(a.State),
			Ready:      a.Applied(),
			TunnelIP:   a.TunnelIP.String(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"assignments": out})
}

func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := s.store.Servers(r.Context(), true)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	type view struct {
		Code        string `json:"code"`
		DisplayName string `json:"display_name"`
		CountryCode string `json:"country_code"`
		Region      string `json:"region"`
		// Load is a coarse hint for the picker. We publish a bucket rather than
		// a peer count so the server list doesn't leak how many users we have.
		Load string `json:"load"`
	}
	out := make([]view, 0, len(servers))
	for _, srv := range servers {
		out = append(out, view{
			Code:        srv.Code,
			DisplayName: srv.DisplayName,
			CountryCode: srv.CountryCode,
			Region:      srv.Region,
			Load:        loadBucket(srv),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": out})
}

func loadBucket(srv model.Server) string {
	if srv.CapacityPeers <= 0 {
		return "unknown"
	}
	switch ratio := float64(srv.LivePeers) / float64(srv.CapacityPeers); {
	case ratio < 0.5:
		return "low"
	case ratio < 0.85:
		return "medium"
	default:
		return "high"
	}
}

func (s *Server) handleListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.Plans(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	type view struct {
		Code         string `json:"code"`
		Name         string `json:"name"`
		DurationDays int    `json:"duration_days"`
		PriceKobo    int64  `json:"price_kobo"`
		PriceNaira   int64  `json:"price_naira"`
		Currency     string `json:"currency"`
		DataCapBytes *int64 `json:"data_cap_bytes,omitempty"`
		DeviceLimit  int    `json:"device_limit"`
		IsFree       bool   `json:"is_free"`
	}
	out := make([]view, 0, len(plans))
	for _, p := range plans {
		out = append(out, view{
			Code:         p.Code,
			Name:         p.Name,
			DurationDays: int(p.Duration.Hours() / 24),
			PriceKobo:    p.PriceKobo,
			PriceNaira:   p.PriceKobo / 100,
			Currency:     p.Currency,
			DataCapBytes: p.DataCapBytes,
			DeviceLimit:  p.DeviceLimit,
			IsFree:       p.IsFree,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": out})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "database": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "database": true})
}

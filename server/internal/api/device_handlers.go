package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/store"
	"github.com/treykys/proxify-vpn/server/internal/wgkey"
)

type registerDeviceRequest struct {
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	PublicKey  string `json:"public_key"`
	AppVersion string `json:"app_version"`
}

// handleRegisterDevice registers the device's WireGuard public key. The private
// key never leaves the phone — we could not hand it over under legal pressure
// even if asked, which is the point.
func (s *Server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	var req registerDeviceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	key, err := wgkey.Validate(req.PublicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_public_key", "public_key must be a base64 WireGuard key")
		return
	}
	if req.Name == "" {
		req.Name = "Android device"
	}
	if req.Platform == "" {
		req.Platform = "android"
	}

	device, err := s.store.UpsertDevice(r.Context(), userID(r), req.Name, req.Platform, key, req.AppVersion)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "key_in_use", "that public key is registered to another account")
			return
		}
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         device.ID,
		"name":       device.Name,
		"platform":   device.Platform,
		"public_key": device.PublicKey,
		"created_at": device.CreatedAt,
	})
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.DevicesByUser(r.Context(), userID(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	type view struct {
		ID       string     `json:"id"`
		Name     string     `json:"name"`
		Platform string     `json:"platform"`
		LastSeen *time.Time `json:"last_seen_at,omitempty"`
	}
	out := make([]view, 0, len(devices))
	for _, d := range devices {
		out = append(out, view{ID: d.ID, Name: d.Name, Platform: d.Platform, LastSeen: d.LastSeenAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	device, err := s.store.DeviceByID(r.Context(), id)
	if err != nil || device.UserID != userID(r) {
		writeError(w, http.StatusNotFound, "not_found", "device not found")
		return
	}

	// Peers come off the edge before the device row is retired, so a removed
	// device stops being able to connect even if the row deletion races.
	if _, err := s.provision.DeprovisionDevice(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	if err := s.store.RevokeDevice(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type rotateKeyRequest struct {
	PublicKey string `json:"public_key"`
}

// handleRotateKey swaps a device's keypair. The app does this when it detects
// key compromise or after a restore onto new hardware; the tunnel IP is
// preserved so the reconnect is a re-handshake, not a renumber.
func (s *Server) handleRotateKey(w http.ResponseWriter, r *http.Request) {
	var req rotateKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	key, err := wgkey.Validate(req.PublicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_public_key", "public_key must be a base64 WireGuard key")
		return
	}

	id := r.PathValue("id")
	device, err := s.store.DeviceByID(r.Context(), id)
	if err != nil || device.UserID != userID(r) {
		writeError(w, http.StatusNotFound, "not_found", "device not found")
		return
	}
	if err := s.store.RotateDeviceKey(r.Context(), id, key); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "key_in_use", "that public key is already registered")
			return
		}
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         id,
		"public_key": key,
		// The client must re-provision to push the new key to the edge; saying
		// so explicitly keeps the contract obvious rather than implied.
		"reprovision_required": true,
	})
}

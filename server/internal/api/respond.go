package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/treykys/proxify-vpn/server/internal/edge"
	"github.com/treykys/proxify-vpn/server/internal/provision"
	"github.com/treykys/proxify-vpn/server/internal/store"
)

// maxBody caps request bodies. Nothing the app sends is large, and the webhook
// path is the only external caller.
const maxBody = 1 << 20

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	// RetryAfter tells the client how long to wait. The Android app uses this
	// rather than inventing its own backoff for provisioning failures.
	RetryAfter int `json:"retry_after_seconds,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already out; nothing useful left to do but stop.
		return
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: code, Message: message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not parse request body")
		return false
	}
	return true
}

// writeDomainError maps domain errors to status codes in one place, so handlers
// don't each invent their own mapping and drift apart.
func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, provision.ErrNotEntitled):
		writeError(w, http.StatusPaymentRequired, "not_entitled",
			"no active subscription — buy a pass to connect")
	case errors.Is(err, provision.ErrDeviceLimit):
		writeError(w, http.StatusConflict, "device_limit",
			"this plan's device limit is reached — remove a device or upgrade")
	case errors.Is(err, provision.ErrNoServer):
		writeError(w, http.StatusServiceUnavailable, "no_server",
			"no server is available right now")
	case errors.Is(err, provision.ErrEdgeUnavailable):
		// The desired state is saved and the reconciler is already retrying, so
		// this is genuinely "try again shortly", not "your request was lost".
		writeJSON(w, http.StatusServiceUnavailable, errorBody{
			Error:      "edge_unavailable",
			Message:    "could not reach a server — retrying in the background",
			RetryAfter: 5,
		})
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "already exists")
	case errors.Is(err, store.ErrNoAddresses):
		writeError(w, http.StatusServiceUnavailable, "server_full", "server is full")
	case errors.Is(err, edge.ErrRejected):
		writeError(w, http.StatusBadGateway, "edge_rejected", "server rejected the configuration")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
	}
}

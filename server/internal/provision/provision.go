// Package provision implements the peer-provisioning glue described in
// docs/provisioning.md — the critical path of the whole product.
//
// The rule the whole package is built around: the database is the source of
// truth for what *should* exist, and every edge interaction is a retryable
// attempt to make reality match it. No request handler is ever the only thing
// standing between a paying user and a working tunnel; if the inline push
// fails, the reconciler finishes the job.
package provision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/edge"
	"github.com/treykys/proxify-vpn/server/internal/model"
	"github.com/treykys/proxify-vpn/server/internal/store"
)

var (
	// ErrNotEntitled means no live prepaid block — the app shows the paywall.
	ErrNotEntitled = errors.New("provision: no active subscription")
	// ErrDeviceLimit means the plan's device allowance is used up.
	ErrDeviceLimit = errors.New("provision: device limit reached")
	// ErrNoServer means no server matched the request (bad code, or none active).
	ErrNoServer = errors.New("provision: no server available")
	// ErrEdgeUnavailable means every candidate edge server refused or timed
	// out. The desired state is recorded; the caller should retry shortly.
	ErrEdgeUnavailable = errors.New("provision: edge unavailable")
)

// Storer is the slice of the store the engine needs. Narrowing it here keeps
// the engine testable and documents exactly which writes provisioning performs.
type Storer interface {
	Entitlement(ctx context.Context, userID string) (model.Entitlement, error)
	DeviceByID(ctx context.Context, id string) (model.Device, error)
	DevicesByUser(ctx context.Context, userID string) ([]model.Device, error)
	Servers(ctx context.Context, onlyActive bool) ([]model.Server, error)
	ServerByCode(ctx context.Context, code string) (model.Server, error)
	ServerByID(ctx context.Context, id string) (model.Server, error)
	EnsureAssignment(ctx context.Context, deviceID, serverID, publicKey string) (model.PeerAssignment, error)
	MarkApplied(ctx context.Context, id string, revision int64) error
	MarkRevoked(ctx context.Context, id string, revision int64) error
	MarkAttemptFailed(ctx context.Context, id, cause string, retryAt time.Time) error
	BeginRevokeForUser(ctx context.Context, userID string) (int, error)
	BeginRevokeForDevice(ctx context.Context, deviceID string) (int, error)
	BeginRevokeOtherServers(ctx context.Context, deviceID, keepServerID string) (int, error)
	DueAssignments(ctx context.Context, limit int) ([]store.DueAssignment, error)
	LivePeersForServer(ctx context.Context, serverID string) ([]model.PeerAssignment, error)
	MarkServerPeersStale(ctx context.Context, serverID string) (int, error)
	ExpiredUserIDs(ctx context.Context, limit int) ([]string, error)
	AssignmentsForDevice(ctx context.Context, deviceID string) ([]model.PeerAssignment, error)
}

type Service struct {
	store Storer
	edge  edge.Client
	log   *slog.Logger
	now   func() time.Time

	// maxInlineAttempts caps how many edge servers a single provisioning
	// request will try before giving up and leaving it to the reconciler. Two
	// is deliberate: one failover is worth the latency, three is not.
	maxInlineAttempts int
}

func New(s Storer, e edge.Client, log *slog.Logger) *Service {
	return &Service{
		store:             s,
		edge:              e,
		log:               log,
		now:               time.Now,
		maxInlineAttempts: 2,
	}
}

// SetClock is a test seam.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

type Request struct {
	UserID   string
	DeviceID string
	// ServerCode selects a specific edge server. Empty or "auto" lets the
	// selector choose.
	ServerCode string
	// ClientCountry is a best-effort hint from the request (Cloudflare's
	// CF-IPCountry). Used only to break ties in auto-selection.
	ClientCountry string
}

// TunnelConfig is everything the app needs to stand up the tunnel. It is
// deliberately a flat, versioned struct: the Android client persists it and
// re-dials from it after a crash without another round trip.
type TunnelConfig struct {
	Version      int    `json:"version"`
	AssignmentID string `json:"assignment_id"`

	// Interface
	Address    string   `json:"address"`     // 10.77.0.5/32
	DNS        []string `json:"dns"`         // v1: 1.1.1.1 (see brief §4)
	MTU        int      `json:"mtu"`         // conservative default; client probes
	AllowedIPs []string `json:"allowed_ips"` // full tunnel

	// Peer
	ServerPublicKey     string `json:"server_public_key"`
	Endpoint            string `json:"endpoint"`
	PersistentKeepalive int    `json:"persistent_keepalive"`

	// Fallbacks the client walks when the primary endpoint is blocked or
	// throttled — see docs/reliability.md (§6 DPI survival + port fallback).
	Fallbacks []Fallback `json:"fallbacks"`

	// Obfuscation params passed through from the server record verbatim.
	Obfuscation map[string]any `json:"obfuscation,omitempty"`

	Server ServerInfo `json:"server"`
	// ExpiresAt is the subscription expiry, so the app can show the countdown
	// and pre-emptively refresh instead of discovering expiry as a drop.
	ExpiresAt time.Time `json:"expires_at"`
}

type Fallback struct {
	Transport string `json:"transport"` // "udp" | "tcp" | "ws-tls"
	Endpoint  string `json:"endpoint"`
	Note      string `json:"note,omitempty"`
}

type ServerInfo struct {
	Code        string `json:"code"`
	DisplayName string `json:"display_name"`
	CountryCode string `json:"country_code"`
	Region      string `json:"region"`
}

// Provision is the §7 flow. It is safe to call repeatedly: an app that calls it
// on every launch gets the same assignment back without touching the edge.
func (s *Service) Provision(ctx context.Context, req Request) (TunnelConfig, error) {
	ent, err := s.store.Entitlement(ctx, req.UserID)
	if err != nil {
		return TunnelConfig{}, fmt.Errorf("entitlement: %w", err)
	}
	if !ent.Active {
		return TunnelConfig{}, ErrNotEntitled
	}

	device, err := s.store.DeviceByID(ctx, req.DeviceID)
	if err != nil {
		return TunnelConfig{}, fmt.Errorf("device: %w", err)
	}
	if device.UserID != req.UserID || device.RevokedAt != nil {
		return TunnelConfig{}, fmt.Errorf("device: %w", store.ErrNotFound)
	}

	if err := s.checkDeviceLimit(ctx, req.UserID, device.ID, ent.DeviceLimit); err != nil {
		return TunnelConfig{}, err
	}

	candidates, err := s.candidates(ctx, req)
	if err != nil {
		return TunnelConfig{}, err
	}

	var lastErr error
	for i, srv := range candidates {
		if i >= s.maxInlineAttempts {
			break
		}
		cfg, err := s.provisionOn(ctx, device, srv, ent)
		if err == nil {
			// Tear down peers on other servers only after the new one is live,
			// so a location switch can never leave the device with nothing.
			if _, err := s.store.BeginRevokeOtherServers(ctx, device.ID, srv.ID); err != nil {
				s.log.Warn("revoke stale assignments", "device", device.ID, "err", err)
			}
			return cfg, nil
		}
		lastErr = err
		if errors.Is(err, edge.ErrRejected) {
			// The box understood us and said no. Failing over would just move
			// a bad request to another box.
			break
		}
		s.log.Warn("edge push failed, trying next server",
			"server", srv.Code, "device", device.ID, "err", err)
	}

	if lastErr == nil {
		return TunnelConfig{}, ErrNoServer
	}
	return TunnelConfig{}, fmt.Errorf("%w: %v", ErrEdgeUnavailable, lastErr)
}

func (s *Service) provisionOn(ctx context.Context, device model.Device, srv model.Server, ent model.Entitlement) (TunnelConfig, error) {
	assignment, err := s.store.EnsureAssignment(ctx, device.ID, srv.ID, device.PublicKey)
	if err != nil {
		return TunnelConfig{}, fmt.Errorf("ensure assignment: %w", err)
	}

	// Fast path: the peer is already installed at the current revision, so a
	// re-launch costs one database round trip and no edge traffic at all.
	if !assignment.Applied() {
		peer := edge.Peer{
			PublicKey: assignment.PublicKey,
			TunnelIP:  assignment.TunnelIP,
			Revision:  assignment.Revision,
			Replaces:  assignment.PrevPublicKey,
		}
		if err := s.edge.ApplyPeer(ctx, srv, peer); err != nil {
			retryAt := s.now().Add(Backoff(assignment.Attempts + 1))
			if mErr := s.store.MarkAttemptFailed(ctx, assignment.ID, err.Error(), retryAt); mErr != nil {
				s.log.Error("record failed attempt", "assignment", assignment.ID, "err", mErr)
			}
			return TunnelConfig{}, err
		}
		if err := s.store.MarkApplied(ctx, assignment.ID, assignment.Revision); err != nil {
			// The peer is installed; only our bookkeeping failed. The
			// reconciler re-applies (idempotently) and fixes the row, so the
			// user is not blocked on this.
			s.log.Error("mark applied", "assignment", assignment.ID, "err", err)
		}
	}

	return s.buildConfig(assignment, srv, ent), nil
}

func (s *Service) buildConfig(a model.PeerAssignment, srv model.Server, ent model.Entitlement) TunnelConfig {
	cfg := TunnelConfig{
		Version:      1,
		AssignmentID: a.ID,
		Address:      a.TunnelIP.String() + "/32",
		DNS:          []string{"1.1.1.1", "1.0.0.1"},
		// 1280 is the IPv6 minimum MTU and survives essentially every path we
		// have seen on Nigerian mobile carriers. The client probes upward from
		// here (docs/reliability.md); this value only has to never stall.
		MTU:             1280,
		AllowedIPs:      []string{"0.0.0.0/0", "::/0"},
		ServerPublicKey: srv.PublicKey,
		Endpoint:        fmt.Sprintf("%s:%d", srv.EndpointHost, srv.EndpointPort),
		// 25s keepalive keeps carrier NAT bindings alive. Nigerian mobile NATs
		// expire UDP mappings fast, and a dead mapping looks exactly like a
		// dropped VPN to the user.
		PersistentKeepalive: 25,
		Obfuscation:         srv.Obfuscation,
		Server: ServerInfo{
			Code:        srv.Code,
			DisplayName: srv.DisplayName,
			CountryCode: srv.CountryCode,
			Region:      srv.Region,
		},
		ExpiresAt: ent.ExpiresAt,
	}
	cfg.Fallbacks = fallbacksFor(srv)
	return cfg
}

// fallbacksFor derives the client's transport ladder from the server's
// obfuscation record. Order matters: cheapest/fastest first, most
// censorship-resistant last.
func fallbacksFor(srv model.Server) []Fallback {
	out := []Fallback{{
		Transport: "udp",
		Endpoint:  fmt.Sprintf("%s:%d", srv.EndpointHost, srv.EndpointPort),
		Note:      "primary",
	}}
	if port, ok := intFromAny(srv.Obfuscation["tcp_port"]); ok {
		out = append(out, Fallback{
			Transport: "tcp",
			Endpoint:  fmt.Sprintf("%s:%d", srv.EndpointHost, port),
			Note:      "udp blocked or throttled",
		})
	}
	if host, ok := srv.Obfuscation["ws_host"].(string); ok && host != "" {
		out = append(out, Fallback{
			Transport: "ws-tls",
			Endpoint:  host + ":443",
			Note:      "deep packet inspection",
		})
	}
	return out
}

func intFromAny(v any) (int, bool) {
	switch n := v.(type) {
	case float64: // JSON numbers decode to float64
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

func (s *Service) checkDeviceLimit(ctx context.Context, userID, deviceID string, limit int) error {
	if limit <= 0 {
		limit = 1
	}
	devices, err := s.store.DevicesByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("devices: %w", err)
	}
	if len(devices) <= limit {
		return nil
	}
	// Over the limit is only an error for a device that isn't already one of
	// the allowed ones: we keep the oldest `limit` devices, so a user who
	// installs on a third phone is told to remove one rather than silently
	// kicking a device they are using.
	for i, d := range devices {
		if d.ID == deviceID {
			if i < limit {
				return nil
			}
			break
		}
	}
	return ErrDeviceLimit
}

// Deprovision removes every peer for a user. Called on expiry and on account
// deletion. It only records intent; the reconciler performs the edge removals,
// which is what makes expiry reliable even when a box is down at the time.
func (s *Service) Deprovision(ctx context.Context, userID string) (int, error) {
	n, err := s.store.BeginRevokeForUser(ctx, userID)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.log.Info("deprovision requested", "user", userID, "assignments", n)
	}
	return n, nil
}

func (s *Service) DeprovisionDevice(ctx context.Context, deviceID string) (int, error) {
	return s.store.BeginRevokeForDevice(ctx, deviceID)
}

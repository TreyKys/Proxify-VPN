// Package edge is the control plane's view of an edge server.
//
// The Client interface is the seam between "the control plane decided a peer
// should exist" and "the box actually has it". Everything behind it is
// replaceable: today an HTTP agent, tomorrow SSH, a queue, or a push from the
// box itself. The provisioning engine only ever talks to this interface, which
// is what lets it be tested without a WireGuard box in the loop.
package edge

import (
	"context"
	"errors"
	"net/netip"

	"github.com/treykys/proxify-vpn/server/internal/model"
)

// Peer is the desired state of one WireGuard peer on one edge server.
type Peer struct {
	PublicKey string     `json:"public_key"`
	TunnelIP  netip.Addr `json:"tunnel_ip"`
	// Replaces is a superseded public key the agent must remove as part of
	// applying this peer. Rotation is one operation, not add-then-hope: the old
	// key must stop working the moment the new one starts.
	Replaces string `json:"replaces,omitempty"`
	// Revision lets the agent ignore a stale write that overtook a newer one,
	// and lets the control plane confirm which desired state was applied.
	Revision int64 `json:"revision"`
}

// Health is what an edge agent reports about itself.
type Health struct {
	OK bool `json:"ok"`
	// BootID changes when the box is rebuilt or its WireGuard state is wiped.
	// A change means every peer we believe is installed is actually gone, so
	// the control plane re-pushes the whole set. This is the difference between
	// "a rebuilt server silently black-holes its users" and "it heals itself".
	BootID     string `json:"boot_id"`
	PeerCount  int    `json:"peer_count"`
	Interface  string `json:"interface"`
	AgentVer   string `json:"agent_version"`
	WireGuard  bool   `json:"wireguard_up"`
	Obfuscator bool   `json:"obfuscator_up"`
}

// ErrUnreachable means we could not talk to the box at all (as opposed to the
// box telling us the request was bad). Only unreachable errors are worth
// failing over to another server for.
var ErrUnreachable = errors.New("edge: unreachable")

// ErrRejected means the agent understood the request and refused it. Retrying
// the same payload will fail the same way, so the reconciler backs off hard
// instead of hammering.
var ErrRejected = errors.New("edge: rejected")

type Client interface {
	// ApplyPeer installs or updates a peer. Must be idempotent: applying the
	// same peer twice is a success, not a conflict.
	ApplyPeer(ctx context.Context, srv model.Server, peer Peer) error
	// RemovePeer deletes a peer by public key. Removing an absent peer is a
	// success — that is the state the caller wanted.
	RemovePeer(ctx context.Context, srv model.Server, publicKey string) error
	// SyncPeers replaces the box's entire peer set. Used after a rebuild.
	SyncPeers(ctx context.Context, srv model.Server, peers []Peer) error
	// Health reports agent liveness and the boot ID.
	Health(ctx context.Context, srv model.Server) (Health, error)
}

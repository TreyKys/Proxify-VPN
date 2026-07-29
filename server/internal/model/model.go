// Package model holds the control-plane domain types shared by the store, the
// provisioning engine and the HTTP layer.
package model

import (
	"net/netip"
	"time"
)

type User struct {
	ID           string
	Email        string
	Phone        string
	PasswordHash string
	Status       string
	CreatedAt    time.Time
}

type Device struct {
	ID         string
	UserID     string
	Name       string
	Platform   string
	PublicKey  string
	AppVersion string
	CreatedAt  time.Time
	LastSeenAt *time.Time
	RevokedAt  *time.Time
}

type Plan struct {
	Code         string
	Name         string
	Duration     time.Duration
	PriceKobo    int64
	Currency     string
	DataCapBytes *int64
	DeviceLimit  int
	IsFree       bool
}

type Subscription struct {
	ID           string
	UserID       string
	PlanCode     string
	Source       string
	StartsAt     time.Time
	ExpiresAt    time.Time
	DataCapBytes *int64
}

// Entitlement is the flattened answer to "what may this user do right now".
type Entitlement struct {
	Active      bool
	PlanCode    string
	ExpiresAt   time.Time
	DeviceLimit int
}

type ServerStatus string

const (
	ServerActive      ServerStatus = "active"
	ServerDraining    ServerStatus = "draining"
	ServerDown        ServerStatus = "down"
	ServerMaintenance ServerStatus = "maintenance"
)

type Server struct {
	ID            string
	Code          string
	DisplayName   string
	CountryCode   string
	Region        string
	EndpointHost  string
	EndpointPort  int
	PublicKey     string
	Obfuscation   map[string]any
	TunnelSubnet  netip.Prefix
	AgentURL      string
	AgentToken    string
	Status        ServerStatus
	CapacityPeers int
	Priority      int
	LastSeenAt    *time.Time
	LivePeers     int
}

type PeerState string

const (
	PeerPending  PeerState = "pending"
	PeerActive   PeerState = "active"
	PeerRevoking PeerState = "revoking"
	PeerRevoked  PeerState = "revoked"
)

// PeerAssignment is the desired state of one device's peer on one edge server.
// See docs/provisioning.md.
type PeerAssignment struct {
	ID        string
	DeviceID  string
	ServerID  string
	PublicKey string
	// PrevPublicKey is the key this assignment supersedes, if any. The edge
	// must remove it in the same operation that installs PublicKey.
	PrevPublicKey   string
	TunnelIP        netip.Addr
	State           PeerState
	Revision        int64
	AppliedRevision int64
	Attempts        int
	LastError       string
	NextAttemptAt   time.Time
	CreatedAt       time.Time
	ActivatedAt     *time.Time
}

// Applied reports whether the edge has confirmed the current desired revision.
func (p PeerAssignment) Applied() bool {
	return p.State == PeerActive && p.AppliedRevision >= p.Revision
}

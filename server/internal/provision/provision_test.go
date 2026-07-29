package provision_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/auth"
	"github.com/treykys/proxify-vpn/server/internal/edge"
	"github.com/treykys/proxify-vpn/server/internal/model"
	"github.com/treykys/proxify-vpn/server/internal/provision"
	"github.com/treykys/proxify-vpn/server/internal/store"
	"github.com/treykys/proxify-vpn/server/internal/testsupport"
)

// These tests exercise the §7 flow end to end against a real database with a
// fake edge. They are the safety net for the piece of the system that, when it
// breaks, means a user paid and got nothing.

type fixture struct {
	t     *testing.T
	store *store.Store
	edge  *edge.Fake
	svc   *provision.Service
	rec   *provision.Reconciler
	ctx   context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := testsupport.NewStore(t)
	fake := edge.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := provision.New(st, fake, log)
	return &fixture{
		t:     t,
		store: st,
		edge:  fake,
		svc:   svc,
		rec:   provision.NewReconciler(svc),
		ctx:   context.Background(),
	}
}

func (f *fixture) user(plan string) string {
	f.t.Helper()
	hash, err := auth.HashPassword("correct-horse-battery")
	if err != nil {
		f.t.Fatal(err)
	}
	u, err := f.store.CreateUser(f.ctx, randEmail(), "", hash)
	if err != nil {
		f.t.Fatalf("create user: %v", err)
	}
	if plan != "" {
		if _, err := f.store.GrantTimeBlock(f.ctx, u.ID, plan, "manual"); err != nil {
			f.t.Fatalf("grant plan: %v", err)
		}
	}
	return u.ID
}

func (f *fixture) device(userID, key string) model.Device {
	f.t.Helper()
	d, err := f.store.UpsertDevice(f.ctx, userID, "Test phone", "android", key, "1.0.0")
	if err != nil {
		f.t.Fatalf("upsert device: %v", err)
	}
	return d
}

func (f *fixture) server(code, country, subnet string, priority int) model.Server {
	f.t.Helper()
	srv, err := f.store.UpsertServer(f.ctx, store.UpsertServerParams{
		Code:          code,
		DisplayName:   code,
		CountryCode:   country,
		Region:        country,
		EndpointHost:  code + ".proxify.test",
		EndpointPort:  51820,
		PublicKey:     serverKey,
		TunnelSubnet:  subnet,
		AgentURL:      "https://" + code + ".proxify.test:8443",
		AgentToken:    "token-" + code,
		Status:        "active",
		CapacityPeers: 500,
		Priority:      priority,
		Obfuscation:   map[string]any{"tcp_port": 443},
	})
	if err != nil {
		f.t.Fatalf("upsert server: %v", err)
	}
	return srv
}

func TestProvisionInstallsPeerAndReturnsConfig(t *testing.T) {
	f := newFixture(t)
	userID := f.user("monthly")
	device := f.device(userID, deviceKeyA)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)

	cfg, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	if !f.edge.HasPeer("de-fsn-1", deviceKeyA) {
		t.Fatal("peer was not pushed to the edge server")
	}
	if cfg.Address != "10.77.0.2/32" {
		t.Errorf("address = %q, want the first allocatable host address", cfg.Address)
	}
	if cfg.Endpoint != "de-fsn-1.proxify.test:51820" {
		t.Errorf("endpoint = %q", cfg.Endpoint)
	}
	if cfg.ServerPublicKey != serverKey {
		t.Errorf("server public key = %q", cfg.ServerPublicKey)
	}
	if cfg.PersistentKeepalive == 0 {
		t.Error("keepalive must be set; carrier NAT drops idle UDP mappings")
	}
	// The TCP fallback must be advertised or the client has nothing to fall
	// back to when UDP is throttled.
	var hasTCP bool
	for _, fb := range cfg.Fallbacks {
		if fb.Transport == "tcp" {
			hasTCP = true
		}
	}
	if !hasTCP {
		t.Error("expected a TCP fallback in the config")
	}
}

func TestProvisionIsIdempotent(t *testing.T) {
	f := newFixture(t)
	userID := f.user("monthly")
	device := f.device(userID, deviceKeyA)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)

	first, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID})
	if err != nil {
		t.Fatalf("first provision: %v", err)
	}
	callsAfterFirst := len(f.edge.Calls)

	// The app calls provision on every launch. Once the peer is live this must
	// cost zero edge traffic and return the identical config.
	second, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID})
	if err != nil {
		t.Fatalf("second provision: %v", err)
	}
	if second.Address != first.Address || second.AssignmentID != first.AssignmentID {
		t.Errorf("config changed between calls: %+v vs %+v", first, second)
	}
	if len(f.edge.Calls) != callsAfterFirst {
		t.Errorf("second provision made %d extra edge calls, want 0",
			len(f.edge.Calls)-callsAfterFirst)
	}
}

func TestProvisionRequiresActiveSubscription(t *testing.T) {
	f := newFixture(t)
	userID := f.user("") // no time block
	device := f.device(userID, deviceKeyA)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)

	_, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID})
	if !errors.Is(err, provision.ErrNotEntitled) {
		t.Fatalf("err = %v, want ErrNotEntitled", err)
	}
	if len(f.edge.Calls) != 0 {
		t.Errorf("unpaid user reached the edge: %v", f.edge.Calls)
	}
}

func TestKeyRotationKeepsAddressAndRepushes(t *testing.T) {
	f := newFixture(t)
	userID := f.user("monthly")
	device := f.device(userID, deviceKeyA)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)

	before, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	if err := f.store.RotateDeviceKey(f.ctx, device.ID, deviceKeyB); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	after, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID})
	if err != nil {
		t.Fatalf("provision after rotation: %v", err)
	}

	if after.Address != before.Address {
		t.Errorf("address changed on key rotation: %s -> %s", before.Address, after.Address)
	}
	if !f.edge.HasPeer("de-fsn-1", deviceKeyB) {
		t.Error("new key was not pushed to the edge")
	}
	if f.edge.HasPeer("de-fsn-1", deviceKeyA) {
		t.Error("old key is still installed after rotation")
	}
}

func TestProvisionFailsOverWhenEdgeIsDown(t *testing.T) {
	f := newFixture(t)
	userID := f.user("monthly")
	device := f.device(userID, deviceKeyA)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10) // preferred, but dead
	f.server("ng-lag-1", "NG", "10.78.0.0/16", 20)
	f.edge.FailFor["de-fsn-1"] = edge.ErrUnreachable

	cfg, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID})
	if err != nil {
		t.Fatalf("provision should have failed over: %v", err)
	}
	if cfg.Server.Code != "ng-lag-1" {
		t.Errorf("landed on %s, want failover to ng-lag-1", cfg.Server.Code)
	}
	if !f.edge.HasPeer("ng-lag-1", deviceKeyA) {
		t.Error("peer missing on the failover server")
	}
}

func TestReconcilerRecoversAfterTotalEdgeOutage(t *testing.T) {
	f := newFixture(t)
	userID := f.user("monthly")
	device := f.device(userID, deviceKeyA)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)
	f.edge.FailWith = edge.ErrUnreachable

	// Every box is down: the user gets an honest 503...
	_, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID})
	if !errors.Is(err, provision.ErrEdgeUnavailable) {
		t.Fatalf("err = %v, want ErrEdgeUnavailable", err)
	}

	// ...but the desired state is recorded, so when the box comes back the
	// reconciler finishes the job without the user doing anything.
	assignments, err := f.store.AssignmentsForDevice(f.ctx, device.ID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("assignments = %v, err = %v; want exactly one recorded", assignments, err)
	}
	if assignments[0].State != model.PeerPending {
		t.Errorf("state = %s, want pending", assignments[0].State)
	}

	f.edge.FailWith = nil
	// The failed attempt scheduled a retry in the future; move the clock past
	// it rather than sleeping through the backoff.
	mustExec(t, f, `UPDATE peer_assignments SET next_attempt_at = now() - interval '1 minute'`)

	if err := f.rec.Once(f.ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !f.edge.HasPeer("de-fsn-1", deviceKeyA) {
		t.Fatal("reconciler did not install the pending peer")
	}

	assignments, err = f.store.AssignmentsForDevice(f.ctx, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !assignments[0].Applied() {
		t.Errorf("assignment not marked applied: %+v", assignments[0])
	}
}

func TestExpiryRemovesPeers(t *testing.T) {
	f := newFixture(t)
	userID := f.user("daily")
	device := f.device(userID, deviceKeyA)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)

	if _, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID}); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !f.edge.HasPeer("de-fsn-1", deviceKeyA) {
		t.Fatal("peer should be installed before expiry")
	}

	// Expire the pass by moving its window into the past.
	mustExec(t, f, `UPDATE subscriptions SET starts_at = now() - interval '2 days',
	                                          expires_at = now() - interval '1 day'`)

	if err := f.rec.Once(f.ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if f.edge.HasPeer("de-fsn-1", deviceKeyA) {
		t.Error("peer is still installed after the pass expired")
	}

	live, err := f.store.LivePeersForServer(f.ctx, f.serverID("de-fsn-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Errorf("live peers = %d, want 0 after expiry", len(live))
	}
}

func TestRenewalAfterExpiryReprovisions(t *testing.T) {
	f := newFixture(t)
	userID := f.user("daily")
	device := f.device(userID, deviceKeyA)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)

	if _, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID}); err != nil {
		t.Fatalf("provision: %v", err)
	}
	mustExec(t, f, `UPDATE subscriptions SET starts_at = now() - interval '2 days',
	                                          expires_at = now() - interval '1 day'`)
	if err := f.rec.Once(f.ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// User tops up again: the peer must come back, which is the path a lapsed
	// user takes every single time they buy another day pass.
	if _, err := f.store.GrantTimeBlock(f.ctx, userID, "daily", "paystack"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID}); err != nil {
		t.Fatalf("re-provision: %v", err)
	}
	if !f.edge.HasPeer("de-fsn-1", deviceKeyA) {
		t.Error("peer was not reinstalled after renewal")
	}
}

func TestSwitchingServersTearsDownTheOldPeer(t *testing.T) {
	f := newFixture(t)
	userID := f.user("monthly")
	device := f.device(userID, deviceKeyA)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)
	f.server("ng-lag-1", "NG", "10.78.0.0/16", 20)

	if _, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID, ServerCode: "de-fsn-1"}); err != nil {
		t.Fatalf("provision: %v", err)
	}
	cfg, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID, ServerCode: "ng-lag-1"})
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if cfg.Server.Code != "ng-lag-1" {
		t.Fatalf("server = %s, want ng-lag-1", cfg.Server.Code)
	}
	// The new peer is live immediately; the old one is torn down by the
	// reconciler, in that order, so the switch never leaves a gap.
	if !f.edge.HasPeer("ng-lag-1", deviceKeyA) {
		t.Error("new peer missing")
	}
	if err := f.rec.Once(f.ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if f.edge.HasPeer("de-fsn-1", deviceKeyA) {
		t.Error("old peer was not removed after switching servers")
	}
}

func TestAddressesAreUniquePerServer(t *testing.T) {
	f := newFixture(t)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)

	seen := map[string]bool{}
	for i := range 5 {
		userID := f.user("monthly")
		device := f.device(userID, testKey(i))
		cfg, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID})
		if err != nil {
			t.Fatalf("provision %d: %v", i, err)
		}
		if seen[cfg.Address] {
			t.Fatalf("address %s handed out twice", cfg.Address)
		}
		seen[cfg.Address] = true
	}
}

func TestResyncRestoresARebuiltServer(t *testing.T) {
	f := newFixture(t)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)
	for i := range 3 {
		userID := f.user("monthly")
		device := f.device(userID, testKey(i))
		if _, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID}); err != nil {
			t.Fatalf("provision %d: %v", i, err)
		}
	}

	// The box is rebuilt and loses every peer.
	f.edge.Reset()
	if f.edge.PeerCount("de-fsn-1") != 0 {
		t.Fatal("precondition: fake edge should be empty")
	}

	srv, err := f.store.ServerByCode(f.ctx, "de-fsn-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.rec.Resync(f.ctx, srv); err != nil {
		t.Fatalf("resync: %v", err)
	}
	if got := f.edge.PeerCount("de-fsn-1"); got != 3 {
		t.Errorf("peer count after resync = %d, want 3", got)
	}
}

func TestRejectedEdgeConfigDoesNotFailOver(t *testing.T) {
	f := newFixture(t)
	userID := f.user("monthly")
	device := f.device(userID, deviceKeyA)
	f.server("de-fsn-1", "DE", "10.77.0.0/16", 10)
	f.server("ng-lag-1", "NG", "10.78.0.0/16", 20)
	f.edge.FailFor["de-fsn-1"] = edge.ErrRejected

	_, err := f.svc.Provision(f.ctx, provision.Request{UserID: userID, DeviceID: device.ID})
	if err == nil {
		t.Fatal("expected an error when the edge rejects the config")
	}
	// A rejection means our request is wrong. Retrying it elsewhere would just
	// spread a bad config across the fleet.
	if f.edge.HasPeer("ng-lag-1", deviceKeyA) {
		t.Error("failed over after a rejection; should have stopped")
	}
}

func (f *fixture) serverID(code string) string {
	f.t.Helper()
	srv, err := f.store.ServerByCode(f.ctx, code)
	if err != nil {
		f.t.Fatal(err)
	}
	return srv.ID
}

func mustExec(t *testing.T, f *fixture, sql string) {
	t.Helper()
	if _, err := f.store.Pool().Exec(f.ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

var (
	// Valid base64 32-byte WireGuard keys.
	serverKey  = "U1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1M="
	deviceKeyA = "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="
	deviceKeyB = "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI="
)

// testKey returns a distinct valid WireGuard public key per index.
func testKey(i int) string {
	raw := []byte("00000000000000000000000000000000")
	raw[0] = byte('a' + i)
	raw[1] = byte('z' - i)
	return base64.StdEncoding.EncodeToString(raw)
}

var emailCounter int

func randEmail() string {
	emailCounter++
	return "user" + itoa(emailCounter) + "-" + itoa(int(time.Now().UnixNano()%100000)) + "@proxify.test"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

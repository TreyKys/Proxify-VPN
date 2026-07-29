package edge

import (
	"context"
	"fmt"
	"sync"

	"github.com/treykys/proxify-vpn/server/internal/model"
)

// Fake is an in-memory Client for tests and for local development without a
// real WireGuard box. It records what the control plane asked for so tests can
// assert on the edge-facing side of §7.
type Fake struct {
	mu sync.Mutex
	// peers[serverCode][publicKey] = applied revision
	peers map[string]map[string]int64
	// FailWith, when non-nil, is returned by every mutating call. Set it to
	// ErrUnreachable to simulate a box that is down.
	FailWith error
	// FailFor makes only one server fail, for failover tests.
	FailFor  map[string]error
	BootIDs  map[string]string
	Calls    []string
	SyncedAt map[string]int
}

func NewFake() *Fake {
	return &Fake{
		peers:    map[string]map[string]int64{},
		FailFor:  map[string]error{},
		BootIDs:  map[string]string{},
		SyncedAt: map[string]int{},
	}
}

func (f *Fake) fail(srv model.Server) error {
	if f.FailWith != nil {
		return fmt.Errorf("%w: fake failure", f.FailWith)
	}
	if err, ok := f.FailFor[srv.Code]; ok && err != nil {
		return fmt.Errorf("%w: fake failure for %s", err, srv.Code)
	}
	return nil
}

func (f *Fake) ApplyPeer(_ context.Context, srv model.Server, peer Peer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "apply:"+srv.Code+":"+peer.PublicKey)
	if err := f.fail(srv); err != nil {
		return err
	}
	if f.peers[srv.Code] == nil {
		f.peers[srv.Code] = map[string]int64{}
	}
	if peer.Replaces != "" {
		delete(f.peers[srv.Code], peer.Replaces)
	}
	f.peers[srv.Code][peer.PublicKey] = peer.Revision
	return nil
}

func (f *Fake) RemovePeer(_ context.Context, srv model.Server, publicKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "remove:"+srv.Code+":"+publicKey)
	if err := f.fail(srv); err != nil {
		return err
	}
	delete(f.peers[srv.Code], publicKey)
	return nil
}

func (f *Fake) SyncPeers(_ context.Context, srv model.Server, peers []Peer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "sync:"+srv.Code)
	if err := f.fail(srv); err != nil {
		return err
	}
	set := map[string]int64{}
	for _, p := range peers {
		set[p.PublicKey] = p.Revision
	}
	f.peers[srv.Code] = set
	f.SyncedAt[srv.Code]++
	return nil
}

func (f *Fake) Health(_ context.Context, srv model.Server) (Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(srv); err != nil {
		return Health{}, err
	}
	bootID := f.BootIDs[srv.Code]
	if bootID == "" {
		bootID = "fake-boot"
	}
	return Health{
		OK:        true,
		BootID:    bootID,
		PeerCount: len(f.peers[srv.Code]),
		WireGuard: true,
	}, nil
}

// HasPeer reports whether the fake box currently holds the key.
func (f *Fake) HasPeer(serverCode, publicKey string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.peers[serverCode][publicKey]
	return ok
}

func (f *Fake) PeerCount(serverCode string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.peers[serverCode])
}

func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.peers = map[string]map[string]int64{}
	f.Calls = nil
	f.FailWith = nil
	f.FailFor = map[string]error{}
}

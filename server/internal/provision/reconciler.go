package provision

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/edge"
	"github.com/treykys/proxify-vpn/server/internal/model"
)

// Reconciler drives desired state onto the edge servers.
//
// This is the component that makes the provisioning promise honest. Anything
// the request path failed to apply — box down, network blip, control plane
// restarted mid-flight — is picked up here and retried until it sticks. It also
// handles the reverse direction: expired users whose peers must come off.
type Reconciler struct {
	svc *Service

	// BatchSize bounds one pass. Small enough that a pass is quick, large
	// enough that a few hundred queued peers clear in seconds.
	BatchSize int
	// ExpiryBatch bounds how many expired users are swept per pass.
	ExpiryBatch int
	// Concurrency is how many edge calls run in parallel per pass. Edge calls
	// are mostly latency, so a handful of goroutines is plenty.
	Concurrency int

	// bootIDs remembers what each server reported so a rebuild is detectable.
	mu      sync.Mutex
	bootIDs map[string]string
}

func NewReconciler(svc *Service) *Reconciler {
	return &Reconciler{
		svc:         svc,
		BatchSize:   100,
		ExpiryBatch: 200,
		Concurrency: 8,
		bootIDs:     map[string]string{},
	}
}

// Run loops until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.Once(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.svc.log.Error("reconcile pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Once runs a single pass: sweep expiries, then apply pending desired state.
func (r *Reconciler) Once(ctx context.Context) error {
	if err := r.sweepExpired(ctx); err != nil {
		return err
	}
	return r.applyDue(ctx)
}

// sweepExpired flips assignments of users with no live time block to 'revoking'.
// Running this before applyDue in the same pass means an expiry noticed now is
// actioned in the same pass rather than one interval later.
func (r *Reconciler) sweepExpired(ctx context.Context) error {
	userIDs, err := r.svc.store.ExpiredUserIDs(ctx, r.ExpiryBatch)
	if err != nil {
		return err
	}
	for _, id := range userIDs {
		n, err := r.svc.store.BeginRevokeForUser(ctx, id)
		if err != nil {
			r.svc.log.Error("expire user", "user", id, "err", err)
			continue
		}
		if n > 0 {
			r.svc.log.Info("subscription expired, revoking peers", "user", id, "peers", n)
		}
	}
	return nil
}

func (r *Reconciler) applyDue(ctx context.Context) error {
	due, err := r.svc.store.DueAssignments(ctx, r.BatchSize)
	if err != nil {
		return err
	}
	if len(due) == 0 {
		return nil
	}

	sem := make(chan struct{}, r.Concurrency)
	var wg sync.WaitGroup
	for _, item := range due {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r.applyOne(ctx, item.Peer, item.Server)
		}()
	}
	wg.Wait()
	return nil
}

func (r *Reconciler) applyOne(ctx context.Context, peer model.PeerAssignment, srv model.Server) {
	var err error
	switch peer.State {
	case model.PeerPending:
		err = r.svc.edge.ApplyPeer(ctx, srv, edge.Peer{
			PublicKey: peer.PublicKey,
			TunnelIP:  peer.TunnelIP,
			Revision:  peer.Revision,
			Replaces:  peer.PrevPublicKey,
		})
		if err == nil {
			err = r.svc.store.MarkApplied(ctx, peer.ID, peer.Revision)
		}
	case model.PeerRevoking:
		err = r.svc.edge.RemovePeer(ctx, srv, peer.PublicKey)
		if err == nil {
			err = r.svc.store.MarkRevoked(ctx, peer.ID, peer.Revision)
		}
	default:
		return
	}

	if err == nil {
		return
	}
	retryAt := r.svc.now().Add(Backoff(peer.Attempts + 1))
	if mErr := r.svc.store.MarkAttemptFailed(ctx, peer.ID, err.Error(), retryAt); mErr != nil {
		r.svc.log.Error("record failed attempt", "assignment", peer.ID, "err", mErr)
	}
	r.svc.log.Warn("reconcile failed",
		"assignment", peer.ID, "server", srv.Code, "state", peer.State,
		"attempts", peer.Attempts+1, "err", err)
}

// HealthSweep polls every server's agent. Its real job is detecting a rebuilt
// box: when the reported boot ID changes, the box has lost its WireGuard state,
// and every peer we believe is live there is actually dead. Rather than wait
// for users to complain, we push the full peer set back.
func (r *Reconciler) HealthSweep(ctx context.Context) error {
	servers, err := r.svc.store.Servers(ctx, false)
	if err != nil {
		return err
	}
	for _, srv := range servers {
		health, err := r.svc.edge.Health(ctx, srv)
		if err != nil {
			r.svc.log.Warn("edge health check failed", "server", srv.Code, "err", err)
			continue
		}
		r.mu.Lock()
		previous, seen := r.bootIDs[srv.ID]
		r.bootIDs[srv.ID] = health.BootID
		r.mu.Unlock()

		if seen && previous == health.BootID {
			continue
		}
		if !seen {
			// First observation after a control-plane restart. Only resync if
			// the box is visibly missing peers, so a rolling deploy of the
			// control plane doesn't trigger a stampede of full syncs.
			live, err := r.svc.store.LivePeersForServer(ctx, srv.ID)
			if err != nil || health.PeerCount >= len(live) {
				continue
			}
		}
		r.svc.log.Warn("edge server state lost, resyncing peers",
			"server", srv.Code, "boot_id", health.BootID, "peer_count", health.PeerCount)
		if err := r.Resync(ctx, srv); err != nil {
			r.svc.log.Error("resync failed", "server", srv.Code, "err", err)
		}
	}
	return nil
}

// Resync pushes the full desired peer set for one server, replacing whatever is
// there. Used after a rebuild and available to the admin API for manual repair.
func (r *Reconciler) Resync(ctx context.Context, srv model.Server) error {
	live, err := r.svc.store.LivePeersForServer(ctx, srv.ID)
	if err != nil {
		return err
	}
	peers := make([]edge.Peer, 0, len(live))
	for _, p := range live {
		peers = append(peers, edge.Peer{
			PublicKey: p.PublicKey,
			TunnelIP:  p.TunnelIP,
			Revision:  p.Revision,
		})
	}
	if err := r.svc.edge.SyncPeers(ctx, srv, peers); err != nil {
		// Leave the rows alone; the next pass retries. Marking them stale here
		// would just add churn to a box we cannot reach.
		return err
	}
	for _, p := range live {
		if p.State == model.PeerPending {
			if err := r.svc.store.MarkApplied(ctx, p.ID, p.Revision); err != nil {
				r.svc.log.Error("mark applied after resync", "assignment", p.ID, "err", err)
			}
		}
	}
	r.svc.log.Info("resynced edge server", "server", srv.Code, "peers", len(peers))
	return nil
}

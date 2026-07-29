# Peer provisioning — the critical path (§7)

This is the piece that, when it breaks, means a user paid and got nothing. It is
built around one rule:

> **The database is the source of truth for what *should* exist. Every edge
> interaction is a retryable attempt to make reality match it.**

No request handler is ever the only thing standing between a paying user and a
working tunnel.

## The state machine

`peer_assignments` holds the desired state of one device's peer on one edge
server.

```
                 EnsureAssignment
   (nothing) ─────────────────────▶ pending ─────────────▶ active
                                      │  ▲   edge confirms    │
                    edge push fails   │  │                    │ expiry, revoke,
                    (retry, backoff)  └──┘                    │ device removed,
                                                              │ server switch
                                                              ▼
                        revoked ◀───────────────────────── revoking
                                    edge confirms removal
```

Only the reconciler performs `pending → active` and `revoking → revoked`. A
request that crashes mid-flight therefore never leaves a half-applied peer: the
worst case is a row sitting in `pending` until the next reconcile pass.

## The flow

```
payment succeeds (Paystack webhook, signature verified)
  └─▶ settle payment exactly once per reference
       └─▶ grant a prepaid time block (stacks onto any live block)

app calls POST /v1/tunnel/provision
  ├─ entitlement check ──────────────────── no live block? 402, show the paywall
  ├─ device belongs to this user, not revoked
  ├─ device limit for the plan
  ├─ select candidate servers (explicit choice first, then auto)
  └─▶ for each candidate, at most 2:
        ├─ EnsureAssignment(device, server, key)   ← allocates a tunnel IP
        ├─ already applied at this revision? return the config, no edge traffic
        ├─ push the peer to the edge agent
        │    ├─ success  → mark applied, return the config
        │    ├─ unreachable → record the failure, back off, try the next server
        │    └─ rejected  → stop. Failing over would spread a bad request
        └─ on success: revoke peers on other servers (after the new one is live)

all candidates failed
  └─▶ 503 with retry_after. The desired state is recorded and the reconciler
      is already retrying — the user's request was not lost.
```

## Why each edge case is handled the way it is

**Idempotency.** The app calls `provision` on every launch. Once a peer is live
at the current revision, the call costs one database round trip and no edge
traffic at all.

**Key rotation.** The assignment keeps its tunnel IP, replaces the public key,
bumps the revision, and records the superseded key in `prev_public_key`. The
edge is told to remove the old key **in the same operation** that installs the
new one. The failure mode we refuse to allow is "both keys work" — a rotation
usually happens *because* a key leaked.

Keeping the IP stable matters too: the client's tun address does not change, so
a reconnect is a re-handshake rather than a renumber.

**Server switching.** The new peer is provisioned first, the old ones are
revoked after. A failed switch can never leave a device with no tunnel at all.

**Expiry.** The reconciler sweeps for users with live peers and no live time
block, flips them to `revoking`, and removes them from the edge. This works even
if the box was down at the moment of expiry — the intent is recorded and retried.

**Server down at payment time.** The assignment is written, the push fails, the
user gets an honest 503 with a retry hint, and the reconciler finishes the job
when the box returns. The user reconnects without doing anything.

**A rebuilt box.** Each agent reports an instance ID that survives reboots but
not rebuilds. When it changes, the control plane knows the box's peer table is
empty and pushes the full desired set back. Without this, rebuilding a server
would silently black-hole everyone on it.

**Webhook replay.** Paystack retries aggressively. `webhook_events` is unique on
`(provider, event_id)` and `payments` is unique on `(provider, provider_ref)`, so
a replayed `charge.success` grants time exactly once.

**Underpayment.** A charge for less than the recorded plan price grants nothing
and logs a warning.

**IP exhaustion.** Allocation takes a row lock on the server and picks the
lowest free host address. Revoked rows are kept rather than deleted so an
address does not go straight back into circulation while an old client is still
sending from it.

## The edge interface

`server/internal/edge.Client` is the seam:

```go
ApplyPeer(ctx, server, peer) error   // idempotent; carries the key it replaces
RemovePeer(ctx, server, publicKey) error  // removing an absent peer succeeds
SyncPeers(ctx, server, peers) error  // full replacement, for a rebuilt box
Health(ctx, server) (Health, error)  // liveness + instance ID
```

Errors are classified into exactly two kinds, and the distinction drives
behaviour:

- `ErrUnreachable` — we could not talk to the box. Worth failing over and
  retrying.
- `ErrRejected` — the box understood us and refused. Retrying the same payload
  anywhere will fail the same way, so we stop.

The interface exists so provisioning can be tested without a WireGuard box in
the loop. `edge.Fake` is the in-memory implementation the tests run against.

## What the tests cover

`server/internal/provision/provision_test.go`, against a real Postgres:

- a provision installs the peer and returns a usable config
- a second provision touches the edge zero times
- an unpaid user never reaches the edge
- rotation keeps the address, installs the new key, removes the old one
- a dead server fails over to another
- a total outage yields 503, and the reconciler completes it afterwards
- expiry removes the peer; renewal puts it back
- switching servers tears down the old peer only after the new one is live
- addresses are never handed out twice
- a resync restores a rebuilt box
- a rejection does not fail over

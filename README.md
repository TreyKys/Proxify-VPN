# Proxify VPN

A mobile VPN engineered for Nigerian network conditions: unstable links, weak
signal, DPI interference, carrier throttling, and cheap low-RAM Android phones.

**The VPN that doesn't drop on Nigerian networks.**

Reliability is the product, not a feature. Every architectural decision here
optimises for staying connected and staying usable on a flaky link.

> On speed: a VPN adds overhead and cannot beat congestion or weak signal. The
> only speed claims this project will make are bypassing content-based carrier
> throttling, and — in v2 — the accelerator layer. Not "makes your internet
> fast".

## Layout

```
server/    Go control plane — auth, prepaid passes, peer provisioning, reconciler
edge/      Go agent + bash provisioning — WireGuard, Xray/Reality, peer management
android/   core (pure Kotlin reliability engine) + app (VpnService, UI)
infra/     local dev database, deployment notes
docs/      the design decisions, and the reasoning behind them
```

## Start here

```bash
make dev-db && make migrate && make server   # control plane on :8080
make test                                    # server + edge + reliability engine
```

Then read, in this order:

1. **`docs/decisions.md`** — stack confirmations, and the four things I'd change
2. **`docs/provisioning.md`** — the critical path: payment → live tunnel → expiry
3. **`docs/reliability.md`** — the differentiator, and how each part is tested
4. **`docs/locations.md`** — the six launch locations, and what they cost
5. **`docs/roadmap.md`** — what to build next, with pass/fail checkpoints

## The shape of it

```
Android app ──obfuscated tunnel──▶ Edge server ──▶ internet
     │                                  ▲
     └──── HTTPS ──▶ Control plane ─────┘
                          │        (adds and removes peers)
                     Postgres
```

Two ideas carry the design:

**Desired state, not commands.** The control plane records which peer *should*
exist on which server; a reconciler makes it true and keeps retrying until it
is. A box being down when someone pays is a delay, not a lost sale.

**Decisions separated from I/O.** The reliability engine is a pure state machine
that takes events and returns actions. The provisioning engine talks to an
interface, not to a box. That is why the behaviour that matters has tests rather
than hopes — 22 for the reliability engine, 12 for the provisioning flow against
a real database.

## Status

| Component | State |
|---|---|
| Control plane, reconciler, payments | built and tested |
| Edge agent, bootstrap script, Reality config | built; agent tested, script not yet run on a real box |
| Fleet inventory — six locations | finalized and verified against a live control plane |
| Reliability engine (§6) | built, 22 tests |
| Android app layer | written; needs an SDK build and a device |
| Accelerator seam (§5) | in place as a pass-through — v2 fills it |

Nothing here has touched real hardware yet. `docs/roadmap.md` Phase 0 is exactly
that, with a checkpoint per step.

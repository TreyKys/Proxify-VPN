# Architecture

```
┌─────────────────┐         obfuscated tunnel        ┌──────────────────┐
│  Android app    │ ═══════════════════════════════▶ │   Edge server    │ ──▶ internet
│                 │   WireGuard/UDP 51820, or        │                  │
│  VpnService     │   WireGuard over Reality/TCP 443 │  wg0 + Xray      │
│  + reliability  │                                  │  + edge-agent    │
│    engine       │                                  └────────┬─────────┘
└────────┬────────┘                                           │
         │  HTTPS                                             │ HTTPS + bearer
         │                                                    │ (peer add/remove)
         ▼                                                    │
┌─────────────────────────────────────────────────────────────┴──────────┐
│                         Control plane (Go)                             │
│  auth · subscriptions · provisioning · reconciler · Paystack webhooks  │
└────────────────────────────────┬───────────────────────────────────────┘
                                 │
                          ┌──────▼──────┐
                          │  Postgres   │
                          └─────────────┘
```

## Repository layout

```
server/    Go control plane          — the §7 glue, auth, payments, reconciler
edge/      Go agent + bash scripts   — per-box provisioning and peer management
android/   core (pure Kotlin) + app  — the §6 reliability engine, then the UI
infra/     local dev, deployment     — Postgres for development, Makefile
docs/      design decisions          — start with decisions.md and provisioning.md
```

## The three things that carry the design

**1. Desired state, not commands.** The control plane writes what *should* be
true and a reconciler makes it true. This is why a box being down at the moment
someone pays is a delay rather than a lost sale. See `docs/provisioning.md`.

**2. Decisions are separated from I/O.** The reliability engine is a pure state
machine that takes events and returns actions; `ProxifyVpnService` performs
them. The provisioning engine talks to an `edge.Client` interface, not to a box.
Both are why the behaviour that matters has tests instead of hopes.

**3. Seams where v2 will cut.** `PacketPipeline` for the accelerator,
`WireGuardBackend` for the transport, `edge.Client` for how boxes are managed.
Each is a no-op or a single implementation today, and each exists because the
alternative is a rewrite later. See `docs/data-path.md`.

## Request paths worth knowing

**Connect.** App → `POST /v1/tunnel/provision` → entitlement check → server
selection → `EnsureAssignment` → push to the edge agent → config returned → app
brings up the tunnel. Idempotent; a re-launch with a live peer touches no edge.

**Payment.** App → `POST /v1/payments/initialize` → Paystack checkout → webhook
→ signature verified → settled once → time block granted. The webhook is the
only thing that grants time; `POST /v1/payments/verify` exists so a user who
returns before the webhook lands is not stuck at a paywall.

**Expiry.** Reconciler sweep → users with live peers and no live block →
`revoking` → edge removal → `revoked`.

**Recovery.** Agent reports a changed instance ID → control plane pushes the
full peer set back. A rebuilt box heals instead of black-holing its users.

## Component docs

- `docs/decisions.md` — stack confirmations and the four things I'd change
- `docs/provisioning.md` — the §7 flow in detail
- `docs/reliability.md` — §6, and how each item maps to code
- `docs/data-path.md` — the §5 accelerator seam
- `docs/logging-policy.md` — what we store, and what enforces it
- `docs/roadmap.md` — implementation order with checkpoints
- `server/README.md`, `edge/README.md`, `android/README.md` — per-component

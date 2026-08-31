# Stack decisions — confirmations and the four things I'd change

Task 1 from the brief: confirm the stack, flag anything I'd change and why.

## Confirmed as briefed

| Choice | Verdict |
|---|---|
| Go + Postgres control plane | Confirmed. The provisioning glue is concurrency and state, which is Go's home ground, and the desired-state model wants real transactions and partial unique indexes. |
| WireGuard as the base tunnel | Confirmed for v1. Fast handshakes are exactly what a reconnect-heavy design needs. |
| Userspace (`wireguard-go`), not kernel | Confirmed. Android has no choice, and owning the userspace path is what keeps the §5 seam open. |
| Prepaid time blocks, no recurring billing | Confirmed, and strongly. Failed-charge churn would be the single largest source of involuntary cancellation here. Blocks **stack** rather than overwrite, so topping up early never destroys time already paid for. |
| Paystack | Confirmed. The webhook is the only thing that grants time; a client-reported success is a hint to re-check, never proof. |
| Never raw EC2/GCP on-demand egress | Confirmed. At ~$0.09/GB with no bundled allowance, metered per-gigabyte egress would invert the unit economics. We host on **AWS Lightsail** instead — its bundled-transfer pricing is the same shape as Hetzner's, just sold by AWS; see `docs/locations.md`. The rule is "no metered on-demand egress," not "no AWS." |
| Bash provisioning until 5+ servers | Confirmed. `edge/scripts/bootstrap.sh` is idempotent and readable top to bottom, which beats a config-management system nobody has debugged at 2am. |
| Android first, Kotlin | Confirmed. minSdk 24 — raising it would cut off the low-end devices this product exists for. |

## Four things I'd change or flag

### 1. The free tier's data cap collides with the no-logs claim

This is the most important item here, and it is a product decision, not a
technical one.

Enforcing a data cap requires counting bytes per user. That is a usage record.
It is not browsing history and it is not a connection log, but a privacy policy
that says "we log almost nothing" while the server maintains per-user byte
counters is a policy that does not match reality — and VPN companies get taken
apart publicly for exactly that gap.

The narrowest honest version, which is what the schema is built for:

- count **aggregate bytes per peer**, never per destination, never with timestamps
- keep a running total and reset it per billing block
- state it plainly in the privacy policy: *"For free accounts we count how much
  data you use, so we can apply the cap. We do not record what you connect to."*

The v1 schema deliberately has **no** usage table yet, because adding one is a
decision that should be made together with the privacy policy wording rather
than discovered later. See `docs/logging-policy.md`.

### 2. §5 and §6 promise things WireGuard cannot deliver

The brief asks for QUIC connection migration (§6) and a QUIC/UDP-based
accelerator-friendly transport (§5), while also specifying WireGuard as the
tunnel (§3). WireGuard has neither connection migration nor multipath, and it
will not grow them.

That is fine, but the implication should be explicit: **the v2 accelerator
means replacing the client↔edge transport, not adding a layer to WireGuard.**
The right v1 hedge is the one already built — own both ends, keep the packet
path behind a pipeline interface (`PacketPipeline`), and avoid anything that
forbids a transport swap. What v1 should *not* do is claim seamless migration
it cannot perform; the app achieves fast handoff recovery through aggressive
re-handshake, which is a different and more modest mechanism.

### 3. Two userspace stacks on a 2GB phone needs measuring before it ships

The DPI-resistant path requires running `wireguard-go` **and** an Xray client on
the handset, with packets crossing between them. On a 2GB device that is two
runtimes, two thread pools, and a copy per packet.

Recommendation: ship UDP-only in Phase 0, measure the obfuscated path's memory
and battery cost on a real low-end device before committing to it as the
default fallback, and keep the ladder ordered so the expensive path is only
reached when the cheap one is actually blocked (which is what
`TransportLadder` does).

Also worth knowing: Reality's `dest`/SNI must be a site that is plausibly
reachable from the client's network. A default of `www.microsoft.com` is fine
in Europe; whether it is plausible traffic from a Nigerian mobile IP is an
empirical question to answer with the Phase 0 box.

### 4. The Lagos box changes the legal picture, not just the latency

A Nigerian egress IP is a real product feature (local content, betting). It also
means operating VPN egress inside the jurisdiction where the entity is
registered: law-enforcement requests land locally and are enforceable locally,
and NDPA obligations apply to the box directly rather than at arm's length.

Worth a specific legal opinion before launch, separate from the general privacy
policy work. The technical mitigation is already in place — the box holds no
logs and no keys that would let us decrypt anything — but "we have nothing to
give you" is a position that needs to be true *and* documented in advance.

## Smaller notes

- **DNS at 1.1.1.1 for v1** — agreed, with the caveat that Cloudflare then sees
  the queries. A resolver on the edge is already Phase 2 in the brief; that is
  the right time for it.
- **New servers register as `draining`**, not `active`. A half-built box should
  never receive a real user, and the promotion should be a deliberate act.
- **The agent needs a real TLS certificate.** `bootstrap.sh` gets one via
  certbot on port 80 rather than a self-signed cert plus a disabled verification
  flag, which is how "encrypted" quietly becomes "not".
- **Rate limits key on the identifier, not the IP.** Most Nigerian mobile users
  share carrier-grade NAT, so IP-keyed limits would lock out whole networks
  while barely inconveniencing an attacker.

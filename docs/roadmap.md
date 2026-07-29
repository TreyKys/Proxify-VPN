# Implementation order and checkpoints

Task 6 from the brief. Written as checkpoints with a pass/fail test, because
"the tunnel works" is not something to decide by feel.

## Where the repo is now

| Piece | State |
|---|---|
| Control plane (auth, subscriptions, provisioning, Paystack) | Built, tested against a real Postgres |
| Reconciler (retry, expiry sweep, resync) | Built, tested |
| Edge agent + `bootstrap.sh` + Reality config | Built; agent unit-tested, script not yet run on a real box |
| Reliability engine (§6) | Built, 22 tests |
| Android app layer | Written; needs an SDK build and a device |
| Accelerator seam (§5) | In place as a pass-through |

Everything not yet exercised on real hardware is exactly what Phase 0 is for.

---

## Phase 0 — the spike (target: one week)

Prove the tunnel and the reliability core end to end. One server, one phone.

**0.1 Resolve the tun-ownership question.** `WireGuardGoBackend` documents two
options. Try option 1 (`GoBackend` owns the tun) first; fall back to the JNI
shim only if the soft kill switch cannot be implemented on top of it.
→ *Checkpoint: a phone reaches the internet through a manually-configured peer.*

**0.2 Bootstrap a real box.** Run `edge/scripts/bootstrap.sh` on a Hetzner
CX22. Register it, promote it to `active`.
→ *Checkpoint: `bootstrap.sh` runs twice in a row with no error and no change on
the second run.*

**0.3 Wire the app to the control plane.** Signup → device registration →
provision → connect, with the real API.
→ *Checkpoint: a fresh install connects in under 10 seconds on a mobile network.*

**0.4 The handoff test — this is the product.** Walk out of WiFi range onto
mobile data mid-download.
→ *Checkpoint: the download continues. Reconnect is under 5 seconds and the app
never shows `UNPROTECTED` during a clean handoff.*

**0.5 The battery test.** Screen off, phone idle for 2 hours, on a 2GB device.
→ *Checkpoint: the tunnel is still up, or has reconnected itself, without the
user touching the phone.*

**0.6 The obfuscation test.** Verify the Reality path carries traffic, and
measure its memory and battery cost on a low-end device (see
`docs/decisions.md` §3).
→ *Checkpoint: a decision, recorded, on whether the obfuscated path ships as the
default fallback in v1.*

---

## MVP (target: 4–6 weeks after Phase 0)

**1. Payments end to end.** Paystack test keys → live keys. Buy a day pass on a
real phone with a real card, then with bank transfer, then with USSD.
→ *Checkpoint: paying grants time within 10 seconds, and paying twice by
accident grants two blocks that stack rather than one that overwrites.*

**2. The lapse-and-renew loop.** Let a day pass expire while connected.
→ *Checkpoint: the peer is removed, the app shows the paywall rather than
retrying forever, and buying again reconnects without a reinstall.*

**3. Second and third servers.** A second EU box and the Lagos box.
→ *Checkpoint: killing the box a user is on gets them onto another one without
them doing anything.*

**4. The reliability soak.** Ten devices, a week, on real Nigerian networks.
Instrument reconnect count, time-to-reconnect, and time spent `UNPROTECTED`.
→ *Checkpoint: median reconnect under 5s; `UNPROTECTED` under 1% of connected
time. These are the numbers the marketing claim rests on — if they don't hold,
fix the product before writing the claim.*

**5. Privacy policy, ToS, and the logging audit.** Walk `docs/logging-policy.md`
against the deployed code and the boxes.
→ *Checkpoint: every claim in the policy maps to a line of code or a config file.*

**6. Play Store submission, and the APK.** Expect VPN-policy review friction;
submit early so the review runs in parallel with the soak. Ship the direct APK
on day one regardless.
→ *Checkpoint: an APK a user can download and install without the Play Store.*

**7. Monitoring.** UptimeRobot on each box and the API; Sentry with IP scrubbing
confirmed; an alert when `peer_assignments` has rows stuck in `pending`.
→ *Checkpoint: kill an edge agent and get paged before a user complains.*

---

## Phase 2 (do not start early)

The accelerator (fill the `PacketPipeline` seam), iOS, multi-region
auto-assignment, recurring billing, self-hosted DNS.

The accelerator is the one with a prerequisite: it needs the soak data from MVP
step 4. Compression and FEC are only worth their CPU cost against measured loss
and measured packet sizes, and guessing at those on a phone budget is how you
ship a feature that makes things slower.

---

## Sequencing note

Phase 0 items are strictly ordered — each depends on the last. MVP items 1–3 can
run in parallel with 4; items 5–7 should start early because policy review and
Play review are wall-clock delays, not work.

The one thing not to reorder: **do not write the reliability marketing claim
before step 4 produces the numbers.** The positioning is "the VPN that doesn't
drop on Nigerian networks", and that is a factual claim we should be able to
defend with data.

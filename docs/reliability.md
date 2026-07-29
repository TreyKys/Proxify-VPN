# Reliability engineering (§6)

Each item in the brief maps to a specific mechanism, and each mechanism has a
test. The tests are in
`android/core/src/test/kotlin/ng/proxify/core/ReliabilityEngineTest.kt` and run
in under a second without an emulator.

## 1. Smart kill switch (soft-fail)

**Failure mode:** the incumbent behaviour — a drop hard-cuts all traffic and the
phone appears dead until the user notices and intervenes.

**Mechanism.** On a drop, traffic is **held** (dropped, not routed) for a grace
window of 15 seconds. The retry schedule is front-loaded — 250ms, 500ms, 1s, 2s
— so several attempts land inside that window. An ordinary blip is therefore
recovered with nothing leaked and nothing visibly broken.

If the window expires without a tunnel:

- **Soft mode (default):** traffic is released directly, the state becomes
  `UNPROTECTED`, and the notification says *"Not protected — your internet is
  working, but traffic is not going through the VPN."*
- **Strict mode (opt-in):** traffic keeps being blocked, indefinitely.

**The tradeoff, stated.** Soft mode can leave traffic unprotected. That is the
deliberate choice, and the mitigation is honesty: the moment protection stops,
the notification and the UI say so in words anyone understands. A user who
prefers losing connectivity to leaking can switch to strict mode in one tap.

The one thing the code will not do is report protection that isn't there — hence
`ConnectionState.isProtected`, which is derived from the traffic policy rather
than set independently, and the test asserting strict mode can never enter
`UNPROTECTED`.

## 2. Seamless reconnection across handoffs

**Failure mode:** WiFi↔cellular and cell↔cell transitions drop the tunnel.

**Mechanism.** Handoffs are detected by comparing *network identity*, not by
waiting for `onLost`. Android will keep a WiFi network marked available while
the user walks out of range, and the tunnel dies long before the system admits
anything changed.

A handoff is treated as expected, not as a failure:

- the attempt counter resets, so we do not serve out a backoff earned on a
  network the phone has already left
- the reconnect is immediate — a `StartTunnel`, not a `ScheduleRetry`
- the transport and MTU learned for the *new* network are applied

Network identity is the framework's network handle, never the SSID: reading
SSIDs requires location permission, and asking a VPN user for their location
"to improve reliability" is how you get uninstalled.

## 3. Battery-optimisation resilience

**Failure mode:** cheap Androids kill background VPNs.

**Mechanism.** A foreground service with a persistent, low-importance,
non-vibrating notification (a VPN that buzzes on every reconnect gets its
notifications disabled, and on these devices that means the service gets
killed). `START_STICKY` so the OS restarts us. First-run guidance sends the user
to the battery-optimisation settings screen — deliberately *not* a
`REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` permission request, which draws Play
Store review scrutiny for the same outcome. `BootReceiver` restores the tunnel
after a reboot, but only if the user had it on and consent is still granted.

## 4. DPI survival and port fallback

**Failure mode:** carriers block or throttle UDP, and DPI fingerprints the
tunnel.

**Mechanism.** A transport ladder: UDP → TCP/443 via the edge's Reality endpoint
→ WebSocket/TLS.

Two properties matter:

- **Two failures before demoting, not one.** A single handshake failure is far
  more often a flaky link than a blocked port, and demoting on every hiccup
  would leave users on the slowest path most of the time.
- **Sticky per network.** A carrier that blocks UDP does it consistently and
  forever. Once TCP/443 is what works on that network, it is what we start with
  there — while home WiFi still starts on UDP. The ladder also wraps, so a user
  pushed onto the slow path is not stuck there after moving to a clean network.

## 5. MTU auto-tuning

**Failure mode:** the one users describe as *"it connects but nothing loads"*.

An MTU that is too large does not fail cleanly: the handshake succeeds, small
requests work, and anything with a full-size packet hangs forever because the
"fragmentation needed" message that should fix it never arrives.

**Mechanism.** Start conservative at 1280 (the IPv6 minimum — every conforming
path must carry it), remembered per network. A stall steps down immediately;
stalls are the expensive failure, so we do not wait for repeated evidence. A
tunnel that stays up for 90 seconds proves the current value and the next rung
up is tried on the *next* connect — never by tearing down a working tunnel.

The server also clamps TCP MSS on the edge (`bootstrap.sh`), which fixes the
same class of problem for TCP from the other direction.

## 6. Aggressive auto-reconnect with clear state

**Mechanism.** Backoff of 250ms → 30s with ±25% jitter. Two deliberate choices:

- **The first retries are nearly immediate.** Most mobile drops are
  sub-second; a policy starting at 5 seconds turns an invisible blip into five
  seconds of dead phone.
- **The ceiling is low (30s).** Ten minutes of backoff is fine on a desktop; here
  it means a user who walked back into coverage stares at a broken connection
  long after the network returned. Network-change events short-circuit the wait
  entirely.

Jitter matters at the fleet level: without it, every device that dropped when a
carrier link flapped retries in lockstep and hammers the edge in waves.

**State the user can act on.** Every `ConnectionStatus` maps to something a
person can understand — `Protected`, `Reconnecting…`, `Not protected`,
`No network`, `Stopped` — rather than to an internal phase of the handshake.

## Server-side contributions

Not everything about reliability lives on the phone:

- **25-second keepalive** in every issued config. Carrier NAT on Nigerian mobile
  networks drops idle UDP mappings within about a minute, and a dead mapping is
  indistinguishable from a dead VPN to the person holding the phone.
- **BBR and `fq`** on the edge. This does not make anyone's link faster; it stops
  a lossy long-haul path from collapsing throughput the way CUBIC does.
- **MSS clamping** on the edge's forward chain.
- **Failover during provisioning** — a dead box means a different box, not a
  dead app.
- **The reconciler**, which means a tunnel that could not be provisioned during
  an outage appears without the user retrying.

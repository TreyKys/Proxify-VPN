# App profiles — making specific apps better, honestly

## What a VPN can and cannot do to speed

It **cannot** make a link faster. Encryption and a detour through a server both
cost something; on a clean, unthrottled connection a VPN is always slightly
slower. It cannot fix weak signal, cell congestion, distance to the tower, or an
exhausted data bundle — which is most of what makes a connection feel slow.

What it can do is stop three specific things from making it slower:

| Mechanism | What it recovers | Where it lives | Status |
|---|---|---|---|
| **Defeat DPI throttling** | Speed the carrier was deliberately withholding from streaming | Reality obfuscation + tunnelling | Built |
| **Stop queue starvation** | Your call, while something else downloads | DSCP marks + `cake` on the edge | Built |
| **Stop pointless detours** | Apps that break or slow down when routed abroad | Per-app bypass | Built |
| **Survive packet loss** | Throughput on a lossy link | BBR now; FEC in v2 | BBR built |

Everything below is one of those four. Nothing here is "makes your internet
fast", and the app never says that.

## The three rules behind every policy

**1. Bypass anything that geo-checks you.** Nigerian bank apps fraud-flag a
sudden foreign IP — at best an OTP challenge, at worst a frozen account.
Nigerian betting sites geo-lock and simply refuse a non-Nigerian exit. Ride
apps need your real location. For these, the tunnel is not a weaker option, it
is a broken one.

**This is why we do not need a Lagos server.** The reason to want a Nigerian
exit IP was betting and banking. Bypassing those apps serves that need better
than a Lagos box would — instantly, at zero infrastructure cost, with no
metered Nigerian bandwidth bill. A Nigerian user in Nigeria already has a
Nigerian IP; they never needed to buy one from us.

**2. Tunnel anything a carrier throttles.** Video and music streaming are what
DPI throttling targets, because it is most of the volume. Encrypted and
obfuscated, the carrier cannot tell a YouTube stream from a file download from
a video call, so it cannot single one out to slow down. This is the one place
we genuinely recover speed — and it is speed the user was already paying for.

**3. Classify everything, so queues stay fair.** A call and a download in the
same tunnel are not equal. The download fills the queue; the call, needing its
packets on time, is destroyed by the wait. Marking them differently and letting
the edge honour the marks is what keeps the call usable. It creates no
bandwidth — it decides who waits.

## The catalog

`android/core/.../apps/AppCatalog.kt` — around 100 apps across banking,
betting, calls, messaging, social, video, music, gaming, commerce, rides,
crypto, work, browsers and system.

| Category | Route | Class | Why |
|---|---|---|---|
| Banking / fintech | **Bypass** | — | A foreign IP gets you challenged or frozen |
| Betting | **Bypass** | — | Geo-locked to Nigeria; a foreign exit is blocked |
| Rides / maps | **Bypass** | — | Location must stay real |
| Gaming | **Bypass** | Realtime | A tunnel only adds ping. We don't pretend otherwise |
| Calls (WhatsApp, Zoom, Meet) | Tunnel | **Realtime** | Throttled by some carriers; ruined by queueing |
| Messaging, social, browsers | Tunnel | Interactive | Someone is waiting on a tap |
| Video, music | Tunnel | **Bulk** | The throttling target; buffers, so it can wait |
| Play Store, backups, photo sync | Tunnel | **Background** | Must never be why a call stutters |

Unknown apps are **tunnelled**. A VPN that quietly leaves unrecognised apps
unprotected is a VPN that lies; the cost of being wrong this way is a
misbehaving app and a one-tap fix, and the cost the other way is traffic the
user believed was protected.

### The escape hatch

The catalog is a curated guess and will be wrong somewhere — a bank we missed, a
package name that changed. `SessionStore.setAppBypassed()` lets a user send any
app direct themselves. That is what makes an incomplete catalog survivable, and
it turns "this app is broken on Proxify" from a support ticket into a tap.

### Verify the package names before launch

35 entries are flagged `needsVerification` — mostly Nigerian banking and betting
apps, where I could not confirm the package name. A wrong name is not dangerous
(the app falls back to the default and is tunnelled) but it does mean the
intended policy silently never applies, and banking is exactly where that hurts.

`AppCatalogTest` prints the full worklist when the suite runs. Confirm with:

```bash
adb shell pm list packages | grep -i <name>
```

This is a Phase 0 task, and a 30-minute one.

## How it works, in two halves

**On the phone.** Bypass is `VpnService.Builder.addDisallowedApplication()` —
those apps never enter the tunnel. Classification writes a DSCP value into each
outbound packet's IP header (`PacketInspector`), repairing the IPv4 checksum
incrementally and preserving the ECN bits, since clobbering those would break
the congestion signalling this whole feature depends on.

Class comes from app identity where we have it, and from packet shape where we
don't — small regular UDP is a call, full-size segments are a download. The
fallback deliberately never promotes anything large to realtime: wrongly
prioritising a stream would starve the calls this exists to protect.

**On the edge.** `qdisc.sh` puts `cake diffserv4` on both interfaces, with
`dual-dsthost`/`dual-srchost` so fairness applies **between users** as well as
between flows. One user's download cannot degrade the box for everyone else. A
systemd unit reapplies it whenever `wg-quick` recreates the interface —
otherwise the qdisc silently vanishes on reboot and the box just gets worse in a
way nobody thinks to check. Kernels without `sch_cake` fall back to `fq_codel`.

### One dependency worth knowing

Per-app bypass works today, under either answer to the tun-ownership question in
`WireGuardGoBackend`. **DSCP marking needs us to own the tun** — if `GoBackend`
owns it, there is no packet stream to run a pipeline over. So Phase 0 task 0.1
decides whether marking ships in v1. The edge-side queueing works regardless,
and delivers most of the benefit on its own, since fair queueing between flows
helps even with every packet marked identically.

The marking stage is the first real inhabitant of the §5 accelerator seam — v1
proof that `PacketPipeline` is a place where things can live, not just a comment
about v2.

## What to measure, so the marketing matches reality

Add to the MVP soak (`docs/roadmap.md` step 4):

1. **Throttled-content throughput, VPN on vs off.** YouTube or TikTok on a
   carrier known to throttle. If this is 2× or better, you have a real,
   defensible claim: *"streams your network slows down play at full speed."*
2. **Latency under load.** Ping while a large download runs, with and without
   `cake` on the edge. This is the "my call didn't break" number.
3. **Baseline overhead.** Plain speed test, VPN on vs off, on an unthrottled
   connection. Expect to be slightly slower. Know the number, so nobody is
   surprised by a screenshot on Twitter.

Do not write the speed claim before these produce numbers. The one thing worse
than not claiming speed is claiming it and having users measure otherwise.

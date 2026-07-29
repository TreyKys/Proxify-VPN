# Android app

Two modules:

| Module | What it is | Buildable without the Android SDK |
|---|---|---|
| `core` | pure Kotlin/JVM — the reliability engine, transport ladder, MTU policy, accelerator seam | **yes** (`gradle :core:test`) |
| `app`  | the Android app — `VpnService`, networking, UI | no |

That split is deliberate. The behaviour that differentiates this product —
surviving handoffs, not black-holing the phone on a blip, falling back when a
carrier blocks UDP — lives in `core`, where it is a pure state machine with 22
tests that run in under a second. `app` is the thin layer that turns Android
events into engine events and executes what the engine returns.

```bash
gradle :core:test          # the tests that matter, no SDK required
gradle :app:assembleDebug  # needs the Android SDK
```

## The reliability engine

`ReliabilityEngine` takes a `TunnelEvent` and returns a list of `TunnelAction`.
It performs no I/O. `ProxifyVpnService` is the only thing that touches Android.

Each behaviour maps to a documented failure mode from the brief:

| Failure mode | Where it lives |
|---|---|
| Hard kill switch stalls the whole phone | `KillSwitchMode.SOFT` + the grace window |
| Drops on WiFi↔cellular handoff | `NetworkSignature` comparison, backoff reset |
| Killed by battery optimisation | foreground service, `START_STICKY`, first-run guidance |
| Carrier blocks/throttles UDP | `TransportLadder`, sticky per network |
| "Connects but nothing loads" | `MtuPolicy` |
| User can't tell what's happening | `ConnectionStatus` → notification and UI, including an explicit "Not protected" |

## The soft kill switch, precisely

On a drop, traffic is **held** (dropped, not routed) for the grace window —
15 seconds by default. The retry schedule is front-loaded so several attempts
land inside that window, which means an ordinary blip is recovered with nothing
leaked and nothing visibly broken.

If the window expires without a tunnel, soft mode releases traffic directly and
the state becomes `UNPROTECTED` — surfaced in the notification and the UI in
plain words. Strict mode keeps blocking indefinitely instead.

This is the central product tradeoff, and it is stated rather than hidden: soft
mode can leave traffic unprotected, and the app says so the moment it happens.

## One open integration decision

`WireGuardGoBackend` documents a choice that needs the SDK and a real handset:
whether `GoBackend` owns the tun (supported path, implemented) or we own it via
a JNI shim (full control over the kill switch). `WireGuardBackend` exists so
that choice is swappable — nothing outside that file depends on it. This is the
first task of Phase 0; see `docs/roadmap.md`.

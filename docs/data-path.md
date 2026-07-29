# The accelerator-ready boundary (§5)

v2 adds a compression + FEC stage between client and edge. The job of v1 is to
make that an **insertion**, not a rewrite.

Three things make that true today.

## 1. We own both ends

The client↔edge segment is entirely ours: our app on one side, our WireGuard
configuration and our agent on the other. Nothing in the data path depends on a
vanilla peer we do not control, so we can change what happens between those two
points without renegotiating with anyone.

## 2. Every packet already goes through a pipeline

`android/core/.../PacketPipeline.kt` defines the stage interface, and v1
installs a pass-through:

```kotlin
interface PacketStage {
    val name: String
    val version: Int
    fun outbound(packet: ByteBuffer): List<ByteBuffer>  // tun  -> tunnel
    fun inbound(packet: ByteBuffer): List<ByteBuffer>   // tunnel -> tun
    fun reset()                                          // on reconnect
}
```

The details that exist specifically so v2 does not need to change this
interface:

- **List return type.** FEC emits parity packets with no 1:1 input, and a
  decoder emits recovered packets with no input at all. A `ByteBuffer?` return
  would have forced a signature change the moment FEC landed.
- **Inbound runs the chain in reverse.** `compress → fec` outbound is
  `fec → compress` inbound. Symmetry by construction; getting it backwards is
  the most likely way to break this later.
- **`reset()` on reconnect.** After a reconnect the far end has no memory of
  what we sent. A stateful stage that assumes otherwise produces garbage that
  looks exactly like a broken network — the hardest possible bug to diagnose.
- **`version`.** Client and edge must refuse a pipeline they do not both
  understand rather than silently corrupting traffic.
- **`isPassThrough`.** v1 skips the pipeline entirely, so the seam costs
  nothing until it does something.

The edge side gets the mirror image of this interface when the accelerator
lands. In v1 the edge does no per-packet processing at all — WireGuard forwards
straight to the internet — so there is nothing yet to mirror.

## 3. Nothing forbids a transport swap

`WireGuardBackend` takes a `Fallback` (transport + endpoint) rather than
assuming UDP-to-a-fixed-port. The transport ladder already carries three kinds.
Replacing the client↔edge transport with a QUIC-based one is a new
implementation of that interface plus a new rung — not surgery on the service.

## What v1 deliberately does not do

- No compression. No FEC. No multipath. The stages are absent, not stubbed with
  half-working code.
- No claim of QUIC connection migration. WireGuard does not have it. v1 recovers
  handoffs by re-handshaking fast, which is a real mechanism with real limits —
  see `docs/decisions.md`.

## When v2 arrives

1. Implement the compression and FEC stages against `PacketStage`.
2. Implement the mirror stages on the edge.
3. Negotiate the pipeline descriptor at handshake; refuse mismatched versions.
4. Install the chain in `WireGuardGoBackend.pipeline` and in the edge's
   equivalent.

No changes to the service, the engine, the control plane, or the schema.

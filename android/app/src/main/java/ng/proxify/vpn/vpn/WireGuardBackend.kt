package ng.proxify.vpn.vpn

import android.os.ParcelFileDescriptor
import ng.proxify.core.Fallback
import ng.proxify.core.PacketPipeline
import ng.proxify.core.Transport
import ng.proxify.core.TunnelConfig

/**
 * The data path.
 *
 * This interface is one half of the accelerator seam from brief §5. Everything
 * between "packet leaves the tun" and "packet enters the tunnel" goes through a
 * [PacketPipeline], which in v1 is a pass-through. When the compression + FEC
 * stage lands in v2, it is installed into the pipeline here and on the edge —
 * no other code moves.
 *
 * The interface also exists so transports are swappable: plain WireGuard over
 * UDP today, WireGuard-over-TCP through the Reality endpoint when a carrier
 * blocks UDP, and whatever we need next without touching the service.
 */
interface WireGuardBackend {

    /**
     * Brings the tunnel up on [tun].
     *
     * Must be idempotent for the same parameters: the reliability engine will
     * ask for a start it may already have.
     *
     * @throws TunnelRejectedException if the edge does not recognise our key —
     *   the caller re-provisions rather than retrying.
     */
    fun start(config: TunnelConfig, fallback: Fallback, mtu: Int, tun: ParcelFileDescriptor)

    /** Tears the tunnel down. Safe to call when already stopped. */
    fun stop()

    /** True while a handshake is current. */
    fun isUp(): Boolean

    /**
     * Bytes since the tunnel came up, for the UI. Deliberately not persisted
     * and not reported anywhere — see docs/logging-policy.md.
     */
    fun transferred(): Transfer

    /** The pipeline packets flow through. v1 installs a pass-through. */
    val pipeline: PacketPipeline
}

data class Transfer(val rxBytes: Long, val txBytes: Long)

/**
 * Builds the `wg-quick`-style config string for the userspace backend.
 *
 * Kept separate from the backend so it can be unit-tested, and because getting
 * these fields wrong produces a tunnel that comes up and carries nothing.
 */
object WireGuardConfigWriter {

    fun build(config: TunnelConfig, privateKey: String, fallback: Fallback, mtu: Int): String {
        val endpoint = fallback.endpoint
        return buildString {
            appendLine("[Interface]")
            appendLine("PrivateKey = $privateKey")
            appendLine("Address = ${config.address}")
            appendLine("DNS = ${config.dns.joinToString(", ")}")
            appendLine("MTU = $mtu")
            appendLine()
            appendLine("[Peer]")
            appendLine("PublicKey = ${config.serverPublicKey}")
            appendLine("AllowedIPs = ${config.allowedIps.joinToString(", ")}")
            appendLine("Endpoint = $endpoint")
            // Keepalive is not optional on Nigerian mobile networks: carrier NAT
            // drops idle UDP mappings within a minute, and a dropped mapping is
            // indistinguishable from a dead VPN to the person holding the phone.
            appendLine("PersistentKeepalive = ${config.persistentKeepalive.coerceAtLeast(15)}")
        }
    }

    /**
     * Whether this transport needs the local obfuscation proxy in front of it.
     * UDP talks to the edge directly; everything else is tunnelled through the
     * Reality endpoint on 443.
     */
    fun needsObfuscationProxy(fallback: Fallback): Boolean = fallback.transport != Transport.UDP
}

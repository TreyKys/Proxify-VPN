package ng.proxify.vpn.vpn

import android.content.Context
import android.os.ParcelFileDescriptor
import com.wireguard.android.backend.Backend
import com.wireguard.android.backend.GoBackend
import com.wireguard.android.backend.Tunnel
import com.wireguard.config.Config
import com.wireguard.config.InetEndpoint
import com.wireguard.config.InetNetwork
import com.wireguard.config.Interface
import com.wireguard.config.Peer
import com.wireguard.crypto.Key
import ng.proxify.core.Fallback
import ng.proxify.core.PacketPipeline
import ng.proxify.core.TunnelConfig
import ng.proxify.core.Transport
import ng.proxify.vpn.data.DeviceKeys

/**
 * The v1 data path: userspace WireGuard via `wireguard-go`.
 *
 * ## Open integration decision — resolve in Phase 0, on a device
 *
 * The soft kill switch (see [ProxifyVpnService.applyTrafficPolicy]) needs *our*
 * service to own the tun file descriptor, so we can keep the interface up while
 * dropping packets. `GoBackend`'s public API instead owns its own `VpnService`
 * and creates the tun itself.
 *
 * There are two ways to reconcile that, and picking between them needs the SDK
 * and a real handset, not a guess:
 *
 *  1. **Let `GoBackend` own the tun**, and implement BLOCK by bringing the
 *     tunnel up with an empty peer set — traffic enters the tun and dies there.
 *     Least code, stays on the library's supported path.
 *  2. **Own the tun ourselves** and drive `wireguard-go` through a small JNI
 *     shim. Full control, at the cost of maintaining that shim.
 *
 * This class implements (1), because it is the supported path and the one that
 * survives library upgrades. [WireGuardBackend] exists precisely so this choice
 * is swappable: nothing outside this file knows which one we picked.
 */
class WireGuardGoBackend(
    context: Context,
    private val keys: DeviceKeys,
) : WireGuardBackend {

    private val backend: Backend = GoBackend(context)
    private val tunnel = ProxifyTunnel()

    override val pipeline: PacketPipeline = PacketPipeline.passThrough()

    override fun start(
        config: TunnelConfig,
        fallback: Fallback,
        mtu: Int,
        tun: ParcelFileDescriptor,
    ) {
        if (fallback.transport != Transport.UDP) {
            // Non-UDP transports go through the local obfuscation proxy, which
            // terminates on 127.0.0.1 and forwards to the edge over Reality.
            // Until that proxy ships, refusing loudly beats silently connecting
            // over a transport the user was told was blocked.
            throw UnsupportedOperationException(
                "transport ${fallback.transport} requires the obfuscation proxy",
            )
        }

        val wgConfig = buildConfig(config, fallback, mtu)
        try {
            backend.setState(tunnel, Tunnel.State.UP, wgConfig)
        } catch (e: Exception) {
            throw TunnelRejectedException(e.message ?: "backend refused the configuration")
        }
    }

    override fun stop() {
        runCatching { backend.setState(tunnel, Tunnel.State.DOWN, null) }
    }

    override fun isUp(): Boolean =
        runCatching { backend.getState(tunnel) == Tunnel.State.UP }.getOrDefault(false)

    override fun transferred(): Transfer {
        val stats = runCatching { backend.getStatistics(tunnel) }.getOrNull()
            ?: return Transfer(0, 0)
        return Transfer(rxBytes = stats.totalRx(), txBytes = stats.totalTx())
    }

    private fun buildConfig(config: TunnelConfig, fallback: Fallback, mtu: Int): Config {
        val iface = Interface.Builder()
            .parsePrivateKey(keys.privateKey())
            .addAddress(InetNetwork.parse(config.address))
            .apply { config.dns.forEach { addDnsServer(java.net.InetAddress.getByName(it)) } }
            .setMtu(mtu)
            .build()

        val peer = Peer.Builder()
            .setPublicKey(Key.fromBase64(config.serverPublicKey))
            .setEndpoint(InetEndpoint.parse(fallback.endpoint.toString()))
            .apply { config.allowedIps.forEach { addAllowedIp(InetNetwork.parse(it)) } }
            // Carrier NAT on Nigerian mobile networks drops idle UDP mappings
            // within about a minute. Without a keepalive the tunnel looks alive
            // and carries nothing, which the user experiences as the internet
            // being broken rather than the VPN.
            .setPersistentKeepalive(config.persistentKeepalive.coerceAtLeast(15))
            .build()

        return Config.Builder().setInterface(iface).addPeer(peer).build()
    }

    private class ProxifyTunnel : Tunnel {
        override fun getName(): String = "proxify"
        override fun onStateChange(newState: Tunnel.State) = Unit
    }
}

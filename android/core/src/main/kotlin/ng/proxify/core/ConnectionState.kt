package ng.proxify.core

/**
 * What the user is told, in their words.
 *
 * The brief's requirement is that the user always knows what is happening
 * instead of silently losing traffic, so every state here maps to something a
 * person can act on — not to an internal phase of the handshake.
 */
enum class ConnectionStatus {
    /** No tunnel, none wanted. */
    DISCONNECTED,

    /** First connection attempt for this session. */
    CONNECTING,

    /** Tunnel up, traffic protected. */
    CONNECTED,

    /**
     * The tunnel dropped and we are re-establishing it. Traffic is held during
     * the grace window so a two-second blip never leaks.
     */
    RECONNECTING,

    /**
     * Reconnection is taking longer than the grace window. In soft mode the
     * user's traffic is flowing **unprotected** so the phone still works; the UI
     * must say so plainly.
     */
    UNPROTECTED,

    /** No usable network at all. Nothing to do but wait. */
    NO_NETWORK,

    /** Terminal: subscription lapsed, credentials rejected, config invalid. */
    FAILED,
}

/** What the VPN service does with packets right now. */
enum class TrafficPolicy {
    /** Packets go through the tunnel. */
    TUNNEL,

    /**
     * Packets are dropped. Either strict mode, or a soft-mode grace window
     * where we would rather stall for a moment than leak.
     */
    BLOCK,

    /**
     * The tunnel is bypassed and traffic goes out the normal interface. Nothing
     * is protected. Only reachable in soft mode, and only after the grace
     * window has expired.
     */
    DIRECT,
}

/**
 * How hard the kill switch bites.
 *
 * Soft is the default, and it is the entire reason this app exists: the number
 * one complaint about incumbent VPNs on Nigerian networks is that a momentary
 * drop bricks the phone's connectivity until the user notices and intervenes.
 *
 * The tradeoff is real and we do not hide it: in [SOFT] mode, once the grace
 * window expires, traffic leaves the device unprotected. The UI says so, and a
 * user who would rather lose connectivity than leak can choose [STRICT].
 */
enum class KillSwitchMode { SOFT, STRICT }

/** A snapshot the UI can render directly. */
data class ConnectionState(
    val status: ConnectionStatus,
    val trafficPolicy: TrafficPolicy,
    val transport: Transport,
    val mtu: Int,
    val serverCode: String?,
    val attempt: Int,
    val network: NetworkSignature?,
) {
    /** True only when traffic is actually going through the tunnel. */
    val isProtected: Boolean get() = trafficPolicy == TrafficPolicy.TUNNEL

    companion object {
        val disconnected = ConnectionState(
            status = ConnectionStatus.DISCONNECTED,
            trafficPolicy = TrafficPolicy.DIRECT,
            transport = Transport.UDP,
            mtu = MtuPolicy.SAFE_MTU,
            serverCode = null,
            attempt = 0,
            network = null,
        )
    }
}

/**
 * Identity of the network the phone is on.
 *
 * Handoffs are detected by comparing signatures, not by listening for
 * "disconnected" — Android will happily keep a dead WiFi network marked
 * available while the user walks out of range, and the tunnel dies long before
 * the system admits the network did.
 */
data class NetworkSignature(val kind: Kind, val id: String) {
    enum class Kind { WIFI, CELLULAR, ETHERNET, OTHER }

    /**
     * A switch between two different networks (WiFi to cellular, or one cell to
     * another) is a handoff: expected, recoverable, and explicitly not a
     * failure to be punished with backoff.
     */
    fun isHandoffFrom(previous: NetworkSignature?): Boolean =
        previous != null && previous != this
}

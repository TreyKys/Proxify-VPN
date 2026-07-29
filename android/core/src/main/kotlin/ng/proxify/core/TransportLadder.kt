package ng.proxify.core

/**
 * Picks how to reach the edge server, and remembers what worked.
 *
 * The problem this solves: a Nigerian carrier that blocks or throttles UDP will
 * do it consistently, on that network, forever. Rediscovering that on every
 * connect costs the user two failed handshakes every single time. So the ladder
 * is **sticky per network** — once TCP/443 is what works on MTN, MTN starts on
 * TCP/443 next time, while WiFi at home still starts on UDP.
 */
class TransportLadder(
    private val memory: MutableMap<NetworkSignature, Transport> = mutableMapOf(),
) {
    private var consecutiveFailures = 0

    /** The transport to try first on [network], given the config's options. */
    fun preferred(config: TunnelConfig, network: NetworkSignature?): Fallback {
        val ladder = config.transportLadder
        val remembered = network?.let { memory[it] }
        return ladder.firstOrNull { it.transport == remembered } ?: ladder.first()
    }

    /**
     * Advances to the next rung after [FAILURES_BEFORE_FALLBACK] consecutive
     * failures on the current one.
     *
     * Two failures, not one: a single handshake failure is far more often a
     * flaky link than a blocked port, and demoting to a slower transport on
     * every hiccup would leave users on the slowest path most of the time.
     */
    fun onFailure(config: TunnelConfig, current: Fallback): Fallback {
        consecutiveFailures++
        if (consecutiveFailures < FAILURES_BEFORE_FALLBACK) return current

        consecutiveFailures = 0
        val ladder = config.transportLadder
        val idx = ladder.indexOfFirst { it.transport == current.transport }
        // Wrapping back to the start matters: a user who was pushed to WS-TLS on
        // a hostile network must retry UDP eventually, or they stay on the slow
        // path forever after switching to a network that never blocked anything.
        val next = if (idx < 0 || idx == ladder.lastIndex) ladder.first() else ladder[idx + 1]
        return next
    }

    /** Records that [fallback] produced a working tunnel on [network]. */
    fun onSuccess(network: NetworkSignature?, fallback: Fallback) {
        consecutiveFailures = 0
        if (network != null) memory[network] = fallback.transport
    }

    /** A handoff is not evidence about the transport; only the counter resets. */
    fun onNetworkChanged() {
        consecutiveFailures = 0
    }

    fun rememberedTransport(network: NetworkSignature): Transport? = memory[network]

    companion object {
        const val FAILURES_BEFORE_FALLBACK = 2
    }
}

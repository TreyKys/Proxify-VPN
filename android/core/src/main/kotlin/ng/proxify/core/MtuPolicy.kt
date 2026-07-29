package ng.proxify.core

/**
 * Chooses the tunnel MTU per network, and remembers what worked.
 *
 * Why this is worth its own class: an MTU that is too large does not fail
 * cleanly. The handshake succeeds, small requests work, and then anything with
 * a full-size packet — a photo upload, a video, a large page — hangs forever
 * because the "fragmentation needed" message that should fix it never arrives.
 * To the user that is not "the VPN is broken", it is "the internet is broken",
 * which is worse. So we start conservative and only grow on evidence.
 *
 * The ladder descends from a typical WireGuard MTU to a value that survives
 * double-encapsulation on the worst paths we expect.
 */
class MtuPolicy(
    private val memory: MutableMap<NetworkSignature, Int> = mutableMapOf(),
) {
    /**
     * The MTU to use on [network]. Falls back to the config's value, which the
     * control plane already sets conservatively.
     */
    fun mtuFor(network: NetworkSignature?, configured: Int): Int {
        val remembered = network?.let { memory[it] }
        return remembered ?: configured.coerceIn(MIN_MTU, LADDER.first())
    }

    /**
     * Called when a tunnel has been stable long enough to trust the current
     * MTU. Returns the value to try next time on this network — one rung up, so
     * a user on a clean network drifts toward better efficiency instead of
     * paying the safe-value overhead forever.
     */
    fun onStable(network: NetworkSignature?, current: Int): Int {
        if (network == null) return current
        val idx = LADDER.indexOfFirst { it <= current }
        val next = if (idx <= 0) LADDER.first() else LADDER[idx - 1]
        memory[network] = next
        return next
    }

    /**
     * Called when traffic stalls at the current MTU: step down and stay there.
     * Stalls are the expensive failure, so we drop immediately rather than
     * requiring repeated evidence.
     */
    fun onStall(network: NetworkSignature?, current: Int): Int {
        val idx = LADDER.indexOfFirst { it <= current }
        val next = when {
            idx < 0 -> SAFE_MTU
            idx == LADDER.lastIndex -> LADDER.last()
            else -> LADDER[idx + 1]
        }
        if (network != null) memory[network] = next
        return next
    }

    fun rememberedMtu(network: NetworkSignature): Int? = memory[network]

    companion object {
        /**
         * Descending rungs. 1280 is the IPv6 minimum MTU: every conforming path
         * must carry it, which makes it the value that is never wrong.
         */
        private val LADDER = intArrayOf(1420, 1380, 1340, 1280, 1240, 1200)

        const val SAFE_MTU = 1280
        const val MIN_MTU = 1200

        /**
         * How long a tunnel must stay up before we treat the current MTU as
         * proven. Long enough that a large transfer has plausibly happened —
         * MTU problems do not show up on the handshake, they show up on the
         * first big packet.
         */
        const val STABLE_AFTER_MILLIS = 90_000L
    }
}

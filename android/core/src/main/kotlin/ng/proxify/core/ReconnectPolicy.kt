package ng.proxify.core

import kotlin.random.Random

/**
 * How long to wait before the next reconnect attempt.
 *
 * Two properties matter more than the exact numbers:
 *
 *  1. **The first few retries are nearly immediate.** Most drops on a mobile
 *     network are sub-second. A policy that starts at 5 seconds turns a blip
 *     the user would never have noticed into five seconds of dead phone.
 *  2. **The ceiling is low.** Ten minutes of backoff on a desktop VPN is fine;
 *     here it means a user who walked back into coverage stares at a broken
 *     connection long after the network came back. We cap at 30 seconds and let
 *     network-change events short-circuit the wait entirely.
 */
class ReconnectPolicy(
    private val random: Random = Random.Default,
) {
    fun delayMillis(attempt: Int): Long {
        if (attempt <= 0) return 0
        val index = (attempt - 1).coerceAtMost(STEPS.lastIndex)
        val base = STEPS[index]
        // ±25% jitter. Without it, every device that dropped when a carrier
        // link flapped retries in lockstep and hammers the edge in waves.
        val jitter = 1.0 + (random.nextDouble() * 2 * JITTER - JITTER)
        return (base * jitter).toLong().coerceAtLeast(0)
    }

    companion object {
        private const val JITTER = 0.25

        /**
         * Retry schedule in milliseconds. Deliberately front-loaded: the first
         * two attempts land inside the grace window, so the common case (a
         * blip) is recovered before the user's traffic is ever released
         * unprotected.
         */
        private val STEPS = longArrayOf(
            250, 500, 1_000, 2_000, 4_000, 8_000, 15_000, 30_000,
        )

        val maxDelayMillis: Long get() = STEPS.last()
    }
}

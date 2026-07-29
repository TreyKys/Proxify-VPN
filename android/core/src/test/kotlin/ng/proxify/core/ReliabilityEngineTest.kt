package ng.proxify.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * These tests are the specification for §6 of the brief. Each one names a
 * documented Nigerian failure mode; if one of them starts failing, the thing
 * that makes this product different is broken.
 */
class ReliabilityEngineTest {

    private val wifi = NetworkSignature(NetworkSignature.Kind.WIFI, "home-wifi")
    private val mtn = NetworkSignature(NetworkSignature.Kind.CELLULAR, "mtn-lte")
    private val glo = NetworkSignature(NetworkSignature.Kind.CELLULAR, "glo-lte")

    private fun config() = TunnelConfig(
        version = 1,
        assignmentId = "assign-1",
        address = "10.77.0.2/32",
        dns = listOf("1.1.1.1"),
        mtu = 1280,
        allowedIps = listOf("0.0.0.0/0", "::/0"),
        serverPublicKey = "server-key",
        endpoint = Endpoint("de-fsn-1.proxify.ng", 51820),
        persistentKeepalive = 25,
        fallbacks = listOf(
            Fallback(Transport.TCP, Endpoint("de-fsn-1.proxify.ng", 443), "udp blocked"),
            Fallback(Transport.WS_TLS, Endpoint("cdn.proxify.ng", 443), "dpi"),
        ),
        server = ServerInfo("de-fsn-1", "Frankfurt", "DE", "eu-central"),
        expiresAtEpochSeconds = 4102444800,
    )

    private fun engine(mode: KillSwitchMode = KillSwitchMode.SOFT) =
        ReliabilityEngine(config = config(), killSwitchMode = mode)

    private fun connected(mode: KillSwitchMode = KillSwitchMode.SOFT): ReliabilityEngine {
        val e = engine(mode)
        e.handle(TunnelEvent.NetworkAvailable(wifi))
        e.handle(TunnelEvent.ConnectRequested)
        e.handle(TunnelEvent.TunnelEstablished)
        return e
    }

    // ------------------------------------------------------- basic connect flow

    @Test
    fun `connects and reports protected traffic`() {
        val e = connected()
        assertEquals(ConnectionStatus.CONNECTED, e.state.status)
        assertEquals(TrafficPolicy.TUNNEL, e.state.trafficPolicy)
        assertTrue(e.state.isProtected)
        assertEquals("de-fsn-1", e.state.serverCode)
    }

    @Test
    fun `traffic is held, not leaked, while the first connection is being made`() {
        val e = engine()
        e.handle(TunnelEvent.NetworkAvailable(wifi))
        val actions = e.handle(TunnelEvent.ConnectRequested)

        assertEquals(TrafficPolicy.BLOCK, e.state.trafficPolicy)
        assertFalse(e.state.isProtected)
        assertTrue(actions.any { it is TunnelAction.StartTunnel })
    }

    // ---------------------------------------------- the soft kill switch (§6.1)

    @Test
    fun `a short blip never leaks and never blacks out the phone`() {
        val e = connected()

        // The tunnel drops. Traffic is held immediately — nothing escapes.
        e.handle(TunnelEvent.TunnelFailed(FailureReason.NETWORK_ERROR))
        assertEquals(ConnectionStatus.RECONNECTING, e.state.status)
        assertEquals(TrafficPolicy.BLOCK, e.state.trafficPolicy)

        // It comes back inside the grace window, as a blip does.
        e.handle(TunnelEvent.RetryTimerFired)
        e.handle(TunnelEvent.TunnelEstablished)

        assertEquals(ConnectionStatus.CONNECTED, e.state.status)
        assertTrue(e.state.isProtected)
    }

    @Test
    fun `the first retries land inside the grace window`() {
        val policy = ReconnectPolicy()
        // Otherwise the soft kill switch would release traffic before the retry
        // schedule has had a real chance to recover a blip.
        val firstThree = (1..3).sumOf { policy.delayMillis(it) }
        assertTrue(
            firstThree < ReliabilityEngine.DEFAULT_GRACE_WINDOW_MILLIS,
            "first three retries take ${firstThree}ms, which exceeds the grace window",
        )
    }

    @Test
    fun `soft mode releases traffic unprotected after the grace window, and says so`() {
        val e = connected(KillSwitchMode.SOFT)
        e.handle(TunnelEvent.TunnelFailed(FailureReason.NETWORK_ERROR))

        val actions = e.handle(TunnelEvent.GraceWindowExpired)

        assertEquals(ConnectionStatus.UNPROTECTED, e.state.status)
        assertEquals(TrafficPolicy.DIRECT, e.state.trafficPolicy)
        assertFalse(e.state.isProtected, "the UI must not be able to claim protection here")
        assertTrue(actions.contains(TunnelAction.SetTrafficPolicy(TrafficPolicy.DIRECT)))
    }

    @Test
    fun `strict mode keeps blocking after the grace window`() {
        val e = connected(KillSwitchMode.STRICT)
        e.handle(TunnelEvent.TunnelFailed(FailureReason.NETWORK_ERROR))
        e.handle(TunnelEvent.GraceWindowExpired)

        assertEquals(TrafficPolicy.BLOCK, e.state.trafficPolicy)
        assertFalse(
            e.state.status == ConnectionStatus.UNPROTECTED,
            "strict mode must never report traffic flowing unprotected",
        )
    }

    @Test
    fun `once unprotected, further failures do not claim to be reconnecting`() {
        val e = connected(KillSwitchMode.SOFT)
        e.handle(TunnelEvent.TunnelFailed(FailureReason.NETWORK_ERROR))
        e.handle(TunnelEvent.GraceWindowExpired)
        e.handle(TunnelEvent.TunnelFailed(FailureReason.HANDSHAKE_TIMEOUT))

        assertEquals(ConnectionStatus.UNPROTECTED, e.state.status)
        assertEquals(TrafficPolicy.DIRECT, e.state.trafficPolicy)
    }

    // ------------------------------------------------- handoffs (§6.2)

    @Test
    fun `a wifi to cellular handoff reconnects immediately without backoff`() {
        val e = connected()

        val actions = e.handle(TunnelEvent.NetworkAvailable(mtn))

        assertEquals(ConnectionStatus.RECONNECTING, e.state.status)
        assertEquals(mtn, e.state.network)
        assertEquals(1, e.state.attempt, "a handoff must not inherit backoff from the old network")
        val start = actions.filterIsInstance<TunnelAction.StartTunnel>().firstOrNull()
        assertNotNull(start, "handoff must trigger an immediate reconnect, not a scheduled retry")
        assertTrue(actions.none { it is TunnelAction.ScheduleRetry })
    }

    @Test
    fun `a handoff clears backoff accumulated on the previous network`() {
        val e = connected()
        repeat(5) { e.handle(TunnelEvent.TunnelFailed(FailureReason.HANDSHAKE_TIMEOUT)) }
        assertTrue(e.state.attempt > 3)

        e.handle(TunnelEvent.NetworkAvailable(glo))

        assertEquals(1, e.state.attempt)
    }

    @Test
    fun `capability churn on the same network does not disturb a working tunnel`() {
        val e = connected()
        val actions = e.handle(TunnelEvent.NetworkAvailable(wifi))

        assertTrue(actions.isEmpty(), "re-announcing the same network must be a no-op")
        assertEquals(ConnectionStatus.CONNECTED, e.state.status)
        assertTrue(e.state.isProtected)
    }

    @Test
    fun `losing the network stops retrying until it comes back`() {
        val e = connected()

        val lost = e.handle(TunnelEvent.NetworkLost)
        assertEquals(ConnectionStatus.NO_NETWORK, e.state.status)
        assertTrue(lost.contains(TunnelAction.CancelTimers))
        assertTrue(lost.none { it is TunnelAction.ScheduleRetry })
        assertNull(e.state.network)

        val back = e.handle(TunnelEvent.NetworkAvailable(mtn))
        assertTrue(back.any { it is TunnelAction.StartTunnel }, "must reconnect when the network returns")
    }

    // ----------------------------------------------- transport fallback (§6.4)

    @Test
    fun `repeated udp failures fall back to tcp on 443`() {
        val e = engine()
        e.handle(TunnelEvent.NetworkAvailable(mtn))
        e.handle(TunnelEvent.ConnectRequested)
        assertEquals(Transport.UDP, e.state.transport)

        // One failure is a flaky link, not a blocked port.
        e.handle(TunnelEvent.TunnelFailed(FailureReason.HANDSHAKE_TIMEOUT))
        assertEquals(Transport.UDP, e.state.transport)

        e.handle(TunnelEvent.TunnelFailed(FailureReason.HANDSHAKE_TIMEOUT))
        assertEquals(Transport.TCP, e.state.transport)
    }

    @Test
    fun `a network that blocks udp is remembered so the next connect skips it`() {
        val ladder = TransportLadder()
        val e = ReliabilityEngine(config = config(), ladder = ladder)
        e.handle(TunnelEvent.NetworkAvailable(mtn))
        e.handle(TunnelEvent.ConnectRequested)
        repeat(2) { e.handle(TunnelEvent.TunnelFailed(FailureReason.HANDSHAKE_TIMEOUT)) }
        e.handle(TunnelEvent.RetryTimerFired)
        e.handle(TunnelEvent.TunnelEstablished)

        assertEquals(Transport.TCP, ladder.rememberedTransport(mtn))
        // ...and a different network is unaffected: home WiFi still gets UDP.
        assertNull(ladder.rememberedTransport(wifi))
    }

    @Test
    fun `the ladder wraps so a user is not stuck on the slow path forever`() {
        val ladder = TransportLadder()
        val cfg = config()
        var current = ladder.preferred(cfg, mtn)
        // Walk off the end of the ladder.
        repeat(TransportLadder.FAILURES_BEFORE_FALLBACK * 3) {
            current = ladder.onFailure(cfg, current)
        }
        assertEquals(Transport.UDP, current.transport)
    }

    // --------------------------------------------------------- MTU tuning (§6.5)

    @Test
    fun `a stalled tunnel drops the mtu and reconnects`() {
        val e = connected()
        val before = e.state.mtu

        val actions = e.handle(TunnelEvent.TrafficStalled)

        assertTrue(e.state.mtu < before, "a stall must lower the MTU")
        assertTrue(actions.any { it is TunnelAction.StartTunnel })
    }

    @Test
    fun `a stable tunnel is never torn down just to try a larger mtu`() {
        val e = connected()
        val actions = e.handle(TunnelEvent.StabilityTimerFired)

        assertTrue(actions.isEmpty(), "chasing efficiency must not disturb a working tunnel")
        assertEquals(ConnectionStatus.CONNECTED, e.state.status)
    }

    @Test
    fun `mtu stays within safe bounds`() {
        val policy = MtuPolicy()
        var mtu = MtuPolicy.SAFE_MTU
        repeat(10) { mtu = policy.onStall(mtn, mtu) }
        assertTrue(mtu >= MtuPolicy.MIN_MTU, "MTU floor breached: $mtu")

        var up = MtuPolicy.SAFE_MTU
        repeat(10) { up = policy.onStable(wifi, up) }
        assertTrue(up <= 1420, "MTU ceiling breached: $up")
    }

    // ------------------------------------------------------------- lifecycle

    @Test
    fun `an expired subscription stops retrying and lets the phone work`() {
        val e = connected(KillSwitchMode.SOFT)
        val actions = e.handle(TunnelEvent.TunnelFailed(FailureReason.SUBSCRIPTION_EXPIRED))

        assertEquals(ConnectionStatus.FAILED, e.state.status)
        assertEquals(TrafficPolicy.DIRECT, e.state.trafficPolicy)
        assertTrue(actions.contains(TunnelAction.RefreshConfig))
        assertTrue(actions.none { it is TunnelAction.ScheduleRetry })

        // And it stays stopped — no retry storm against a server that will keep
        // saying no.
        assertTrue(e.handle(TunnelEvent.RetryTimerFired).isEmpty())
    }

    @Test
    fun `a rejected peer asks the control plane for a fresh config`() {
        val e = connected()
        val actions = e.handle(TunnelEvent.TunnelFailed(FailureReason.PEER_REJECTED))

        assertTrue(
            actions.contains(TunnelAction.RefreshConfig),
            "a key the edge does not know is fixed by re-provisioning, not by retrying",
        )
        assertTrue(actions.any { it is TunnelAction.ScheduleRetry })
    }

    @Test
    fun `disconnecting is final until the user asks again`() {
        val e = connected()
        e.handle(TunnelEvent.DisconnectRequested)

        assertEquals(ConnectionStatus.DISCONNECTED, e.state.status)
        assertEquals(TrafficPolicy.DIRECT, e.state.trafficPolicy)

        // Network churn must not silently reconnect a user who chose to stop.
        assertTrue(e.handle(TunnelEvent.NetworkAvailable(mtn)).isEmpty())
        assertTrue(e.handle(TunnelEvent.RetryTimerFired).isEmpty())
        assertEquals(ConnectionStatus.DISCONNECTED, e.state.status)
    }

    @Test
    fun `connecting without a config asks for one instead of failing`() {
        val e = ReliabilityEngine(config = null)
        e.handle(TunnelEvent.NetworkAvailable(wifi))
        val actions = e.handle(TunnelEvent.ConnectRequested)

        assertTrue(actions.contains(TunnelAction.RefreshConfig))
        assertEquals(ConnectionStatus.CONNECTING, e.state.status)
    }

    @Test
    fun `backoff is bounded so returning coverage recovers quickly`() {
        val policy = ReconnectPolicy()
        val worst = (1..50).maxOf { policy.delayMillis(it) }
        assertTrue(
            worst <= ReconnectPolicy.maxDelayMillis * 1.3,
            "backoff grew to ${worst}ms; a user walking back into coverage would wait too long",
        )
    }
}

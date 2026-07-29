package ng.proxify.core

/** Things that happen to a tunnel. Fed in by the Android VPN service. */
sealed interface TunnelEvent {
    /** The user asked to connect. */
    data object ConnectRequested : TunnelEvent

    /** The user asked to disconnect. Nothing auto-reconnects after this. */
    data object DisconnectRequested : TunnelEvent

    /** A usable network appeared, or the phone moved to a different one. */
    data class NetworkAvailable(val network: NetworkSignature) : TunnelEvent

    /** No usable network at all. */
    data object NetworkLost : TunnelEvent

    /** A handshake completed; traffic is flowing through the tunnel. */
    data object TunnelEstablished : TunnelEvent

    /** The tunnel failed or never came up. */
    data class TunnelFailed(val reason: FailureReason) : TunnelEvent

    /** The scheduled retry is due. */
    data object RetryTimerFired : TunnelEvent

    /** We have been trying to (re)connect for longer than the grace window. */
    data object GraceWindowExpired : TunnelEvent

    /** The tunnel has been up long enough to trust the current MTU. */
    data object StabilityTimerFired : TunnelEvent

    /**
     * The tunnel is up but large packets are not getting through — the classic
     * MTU symptom, and the one users describe as "it connects but nothing
     * loads".
     */
    data object TrafficStalled : TunnelEvent
}

enum class FailureReason {
    HANDSHAKE_TIMEOUT,
    NETWORK_ERROR,

    /** The edge rejected us: our key isn't installed (yet). Refresh the config. */
    PEER_REJECTED,

    /** Subscription lapsed. Terminal until the user pays. */
    SUBSCRIPTION_EXPIRED,

    /** The config we hold is unusable. Terminal until refreshed. */
    CONFIG_INVALID,
    ;

    val isTerminal: Boolean
        get() = this == SUBSCRIPTION_EXPIRED || this == CONFIG_INVALID
}

/** What the service should do. The engine never performs I/O itself. */
sealed interface TunnelAction {
    data class StartTunnel(val fallback: Fallback, val mtu: Int) : TunnelAction
    data object StopTunnel : TunnelAction
    data class SetTrafficPolicy(val policy: TrafficPolicy) : TunnelAction
    data class ScheduleRetry(val delayMillis: Long) : TunnelAction
    data class ScheduleGraceExpiry(val delayMillis: Long) : TunnelAction
    data class ScheduleStabilityCheck(val delayMillis: Long) : TunnelAction
    data object CancelTimers : TunnelAction

    /** Ask the control plane for a fresh tunnel config. */
    data object RefreshConfig : TunnelAction
}

/**
 * The reliability state machine — the part of this product that is the product.
 *
 * It is deliberately free of Android, coroutines, and I/O: it takes events and
 * returns actions. That is what makes the behaviour the brief cares about —
 * surviving handoffs, not black-holing the phone on a blip, falling back when a
 * carrier blocks UDP — something we can assert on in tests instead of hoping
 * for on a real network.
 *
 * @param graceWindowMillis how long traffic is held before soft mode chooses
 *   usability over privacy. See [KillSwitchMode].
 */
class ReliabilityEngine(
    private var config: TunnelConfig? = null,
    val killSwitchMode: KillSwitchMode = KillSwitchMode.SOFT,
    private val reconnect: ReconnectPolicy = ReconnectPolicy(),
    private val ladder: TransportLadder = TransportLadder(),
    private val mtuPolicy: MtuPolicy = MtuPolicy(),
    private val graceWindowMillis: Long = DEFAULT_GRACE_WINDOW_MILLIS,
) {
    var state: ConnectionState = ConnectionState.disconnected
        private set

    /** User intent. Survives network churn; only the user clears it. */
    private var wantsTunnel = false

    private var currentFallback: Fallback? = null
    private var graceRunning = false

    fun updateConfig(newConfig: TunnelConfig) {
        config = newConfig
    }

    fun currentConfig(): TunnelConfig? = config

    fun handle(event: TunnelEvent): List<TunnelAction> = when (event) {
        TunnelEvent.ConnectRequested -> onConnectRequested()
        TunnelEvent.DisconnectRequested -> onDisconnectRequested()
        is TunnelEvent.NetworkAvailable -> onNetworkAvailable(event.network)
        TunnelEvent.NetworkLost -> onNetworkLost()
        TunnelEvent.TunnelEstablished -> onEstablished()
        is TunnelEvent.TunnelFailed -> onFailed(event.reason)
        TunnelEvent.RetryTimerFired -> onRetry()
        TunnelEvent.GraceWindowExpired -> onGraceExpired()
        TunnelEvent.StabilityTimerFired -> onStable()
        TunnelEvent.TrafficStalled -> onStalled()
    }

    // ------------------------------------------------------------------ intent

    private fun onConnectRequested(): List<TunnelAction> {
        wantsTunnel = true
        val cfg = config ?: run {
            state = state.copy(status = ConnectionStatus.CONNECTING, attempt = 0)
            return listOf(TunnelAction.RefreshConfig)
        }
        val network = state.network
            ?: return holdWithoutNetwork()

        val fallback = ladder.preferred(cfg, network)
        val mtu = mtuPolicy.mtuFor(network, cfg.mtu)
        currentFallback = fallback
        state = state.copy(
            status = ConnectionStatus.CONNECTING,
            trafficPolicy = TrafficPolicy.BLOCK,
            transport = fallback.transport,
            mtu = mtu,
            serverCode = cfg.server.code,
            attempt = 1,
        )
        return startTunnelActions(fallback, mtu, startGrace = true)
    }

    private fun onDisconnectRequested(): List<TunnelAction> {
        wantsTunnel = false
        graceRunning = false
        currentFallback = null
        state = ConnectionState.disconnected.copy(network = state.network)
        return listOf(
            TunnelAction.CancelTimers,
            TunnelAction.StopTunnel,
            // Disconnecting is an explicit choice to stop being protected, so
            // traffic flows normally — even in strict mode. Strict mode governs
            // unexpected drops, not the user's own decision.
            TunnelAction.SetTrafficPolicy(TrafficPolicy.DIRECT),
        )
    }

    // ----------------------------------------------------------------- network

    private fun onNetworkAvailable(network: NetworkSignature): List<TunnelAction> {
        val handoff = network.isHandoffFrom(state.network)
        val wasOffline = state.status == ConnectionStatus.NO_NETWORK
        state = state.copy(network = network)

        if (!wantsTunnel) return emptyList()
        val cfg = config ?: return listOf(TunnelAction.RefreshConfig)

        if (!handoff && !wasOffline && state.status == ConnectionStatus.CONNECTED) {
            // Same network, already connected: nothing to do. Android emits
            // capability changes constantly and reacting would mean tearing
            // down a working tunnel for no reason.
            return emptyList()
        }

        // A handoff is expected, not a failure: the attempt counter resets so we
        // reconnect immediately instead of serving out a backoff earned on a
        // network the phone has already left.
        ladder.onNetworkChanged()
        val fallback = ladder.preferred(cfg, network)
        val mtu = mtuPolicy.mtuFor(network, cfg.mtu)
        currentFallback = fallback
        state = state.copy(
            status = if (wasOffline) ConnectionStatus.CONNECTING else ConnectionStatus.RECONNECTING,
            trafficPolicy = TrafficPolicy.BLOCK,
            transport = fallback.transport,
            mtu = mtu,
            attempt = 1,
        )
        return startTunnelActions(fallback, mtu, startGrace = true)
    }

    private fun onNetworkLost(): List<TunnelAction> {
        state = state.copy(
            status = if (wantsTunnel) ConnectionStatus.NO_NETWORK else ConnectionStatus.DISCONNECTED,
            trafficPolicy = TrafficPolicy.BLOCK,
            network = null,
        )
        graceRunning = false
        // No point burning retries against a network that isn't there; the next
        // NetworkAvailable is what restarts us.
        return listOf(TunnelAction.CancelTimers, TunnelAction.StopTunnel)
    }

    // ------------------------------------------------------------------ tunnel

    private fun onEstablished(): List<TunnelAction> {
        val fallback = currentFallback
        if (fallback != null) ladder.onSuccess(state.network, fallback)
        graceRunning = false
        state = state.copy(
            status = ConnectionStatus.CONNECTED,
            trafficPolicy = TrafficPolicy.TUNNEL,
            attempt = 0,
        )
        return listOf(
            TunnelAction.CancelTimers,
            TunnelAction.SetTrafficPolicy(TrafficPolicy.TUNNEL),
            TunnelAction.ScheduleStabilityCheck(MtuPolicy.STABLE_AFTER_MILLIS),
        )
    }

    private fun onFailed(reason: FailureReason): List<TunnelAction> {
        if (reason.isTerminal) {
            wantsTunnel = false
            graceRunning = false
            state = state.copy(
                status = ConnectionStatus.FAILED,
                // A lapsed subscription is not a security event: let the phone
                // work while the user goes and buys a pass.
                trafficPolicy = if (killSwitchMode == KillSwitchMode.STRICT) {
                    TrafficPolicy.BLOCK
                } else {
                    TrafficPolicy.DIRECT
                },
                attempt = 0,
            )
            return listOf(
                TunnelAction.CancelTimers,
                TunnelAction.StopTunnel,
                TunnelAction.SetTrafficPolicy(state.trafficPolicy),
                TunnelAction.RefreshConfig,
            )
        }

        if (!wantsTunnel) return emptyList()
        val cfg = config ?: return listOf(TunnelAction.RefreshConfig)

        val previous = currentFallback ?: ladder.preferred(cfg, state.network)
        val next = ladder.onFailure(cfg, previous)
        currentFallback = next

        val attempt = state.attempt + 1
        // Once we have told the user they are unprotected, stay on that message
        // until we actually reconnect. Flipping back to "reconnecting" would
        // imply protection that isn't there.
        val nextStatus =
            if (state.status == ConnectionStatus.UNPROTECTED) ConnectionStatus.UNPROTECTED
            else ConnectionStatus.RECONNECTING
        state = state.copy(status = nextStatus, transport = next.transport, attempt = attempt)

        val actions = mutableListOf<TunnelAction>()
        if (!graceRunning && state.status != ConnectionStatus.UNPROTECTED) {
            graceRunning = true
            state = state.copy(trafficPolicy = TrafficPolicy.BLOCK)
            actions += TunnelAction.SetTrafficPolicy(TrafficPolicy.BLOCK)
            actions += TunnelAction.ScheduleGraceExpiry(graceWindowMillis)
        }
        actions += TunnelAction.ScheduleRetry(reconnect.delayMillis(attempt))
        // A rejected peer usually means the edge lost our key (rebuilt box, or
        // provisioning that hadn't landed yet). Re-asking the control plane is
        // what fixes it; retrying the same handshake forever is not.
        if (reason == FailureReason.PEER_REJECTED) actions += TunnelAction.RefreshConfig
        return actions
    }

    private fun onRetry(): List<TunnelAction> {
        if (!wantsTunnel) return emptyList()
        val cfg = config ?: return listOf(TunnelAction.RefreshConfig)
        if (state.network == null) return holdWithoutNetwork()

        val fallback = currentFallback ?: ladder.preferred(cfg, state.network)
        currentFallback = fallback
        state = state.copy(transport = fallback.transport)
        return startTunnelActions(fallback, state.mtu, startGrace = false)
    }

    private fun onGraceExpired(): List<TunnelAction> {
        graceRunning = false
        if (!wantsTunnel || state.status == ConnectionStatus.CONNECTED) return emptyList()

        return when (killSwitchMode) {
            KillSwitchMode.SOFT -> {
                // The moment we stop protecting traffic, the user is told. An
                // unprotected connection the user believes is protected would be
                // worse than either honest alternative.
                state = state.copy(
                    status = ConnectionStatus.UNPROTECTED,
                    trafficPolicy = TrafficPolicy.DIRECT,
                )
                listOf(TunnelAction.SetTrafficPolicy(TrafficPolicy.DIRECT))
            }

            KillSwitchMode.STRICT -> {
                // Strict mode keeps blocking, and keeps trying.
                state = state.copy(trafficPolicy = TrafficPolicy.BLOCK)
                emptyList()
            }
        }
    }

    // --------------------------------------------------------------------- MTU

    private fun onStable(): List<TunnelAction> {
        // Only remembered for next time — an MTU change means renegotiating the
        // interface, and tearing down a working tunnel to chase efficiency is
        // exactly the kind of cleverness that loses users.
        mtuPolicy.onStable(state.network, state.mtu)
        return emptyList()
    }

    private fun onStalled(): List<TunnelAction> {
        val lower = mtuPolicy.onStall(state.network, state.mtu)
        if (lower == state.mtu) return emptyList()

        state = state.copy(status = ConnectionStatus.RECONNECTING, mtu = lower)
        val fallback = currentFallback ?: return emptyList()
        return listOf(
            TunnelAction.StopTunnel,
            TunnelAction.StartTunnel(fallback, lower),
        )
    }

    // ------------------------------------------------------------------ helpers

    private fun startTunnelActions(
        fallback: Fallback,
        mtu: Int,
        startGrace: Boolean,
    ): List<TunnelAction> {
        val actions = mutableListOf<TunnelAction>(
            TunnelAction.SetTrafficPolicy(state.trafficPolicy),
            TunnelAction.StartTunnel(fallback, mtu),
        )
        if (startGrace && !graceRunning) {
            graceRunning = true
            actions += TunnelAction.ScheduleGraceExpiry(graceWindowMillis)
        }
        return actions
    }

    private fun holdWithoutNetwork(): List<TunnelAction> {
        state = state.copy(
            status = ConnectionStatus.NO_NETWORK,
            trafficPolicy = TrafficPolicy.BLOCK,
        )
        return listOf(TunnelAction.SetTrafficPolicy(TrafficPolicy.BLOCK))
    }

    companion object {
        /**
         * 15 seconds. Long enough that the front-loaded retry schedule gets
         * several attempts in — so an ordinary blip is recovered while traffic
         * is still held, and nothing leaks. Short enough that a user in a lift
         * or a dead spot is not staring at a dead phone.
         */
        const val DEFAULT_GRACE_WINDOW_MILLIS = 15_000L
    }
}

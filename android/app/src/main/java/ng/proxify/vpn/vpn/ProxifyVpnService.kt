package ng.proxify.vpn.vpn

import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import ng.proxify.core.ConnectionState
import ng.proxify.core.ConnectionStatus
import ng.proxify.core.Fallback
import ng.proxify.core.KillSwitchMode
import ng.proxify.core.FailureReason
import ng.proxify.core.ReliabilityEngine
import ng.proxify.core.TrafficPolicy
import ng.proxify.core.TunnelAction
import ng.proxify.core.TunnelConfig
import ng.proxify.core.TunnelEvent
import ng.proxify.vpn.ProxifyApplication
import ng.proxify.vpn.ui.MainActivity

/**
 * The VPN service.
 *
 * All the decisions live in [ReliabilityEngine], which is pure Kotlin and
 * tested. This class does exactly three things: turn Android events into engine
 * events, execute the actions the engine returns, and keep itself alive.
 *
 * Keeping it that thin is deliberate — the logic that has to survive a Nigerian
 * commute is not something we want to be debugging through an emulator.
 */
class ProxifyVpnService : VpnService() {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var engine: ReliabilityEngine
    private lateinit var networkMonitor: NetworkMonitor
    private lateinit var notifications: TunnelNotifications
    private lateinit var backend: WireGuardBackend
    private lateinit var repository: TunnelRepository

    private var tunInterface: ParcelFileDescriptor? = null
    private var retryJob: Job? = null
    private var graceJob: Job? = null
    private var stabilityJob: Job? = null

    private val _state = MutableStateFlow(ConnectionState.disconnected)
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    override fun onCreate() {
        super.onCreate()
        val app = application as ProxifyApplication
        repository = app.tunnelRepository
        backend = app.wireGuardBackend
        notifications = TunnelNotifications(this)
        engine = ReliabilityEngine(
            config = repository.cachedConfig(),
            killSwitchMode = repository.killSwitchMode(),
        )
        networkMonitor = NetworkMonitor(this) { event -> dispatch(event) }
        networkMonitor.start()
        instance = this
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_DISCONNECT -> {
                dispatch(TunnelEvent.DisconnectRequested)
                stopSelf()
                return START_NOT_STICKY
            }

            else -> {
                startForeground(
                    TunnelNotifications.NOTIFICATION_ID,
                    notifications.build(_state.value, disconnectIntent(), openAppIntent()),
                )
                dispatch(TunnelEvent.ConnectRequested)
            }
        }
        // START_STICKY is the whole battery-resilience story: when a cheap
        // Android kills us to reclaim memory, the system brings the service
        // back and we re-establish rather than leaving the user silently
        // unprotected with a VPN icon still showing.
        return START_STICKY
    }

    override fun onRevoke() {
        // Another VPN app took over, or the user revoked consent. Do not fight
        // it — a VPN that keeps grabbing the tunnel back is a VPN that gets
        // uninstalled.
        Log.i(TAG, "VPN consent revoked")
        dispatch(TunnelEvent.DisconnectRequested)
        stopSelf()
    }

    override fun onDestroy() {
        networkMonitor.stop()
        closeTun()
        backend.stop()
        scope.cancel()
        instance = null
        super.onDestroy()
    }

    // ---------------------------------------------------------------- dispatch

    /** Feeds one event to the engine and executes what it asks for. */
    @Synchronized
    fun dispatch(event: TunnelEvent) {
        val actions = engine.handle(event)
        _state.value = engine.state
        notifications.update(engine.state, disconnectIntent(), openAppIntent())
        actions.forEach(::execute)
    }

    private fun execute(action: TunnelAction) {
        when (action) {
            is TunnelAction.StartTunnel -> startTunnel(action.fallback, action.mtu)
            TunnelAction.StopTunnel -> backend.stop()
            is TunnelAction.SetTrafficPolicy -> applyTrafficPolicy(action.policy)
            is TunnelAction.ScheduleRetry -> scheduleRetry(action.delayMillis)
            is TunnelAction.ScheduleGraceExpiry -> scheduleGrace(action.delayMillis)
            is TunnelAction.ScheduleStabilityCheck -> scheduleStability(action.delayMillis)
            TunnelAction.CancelTimers -> cancelTimers()
            TunnelAction.RefreshConfig -> refreshConfig()
        }
    }

    // ------------------------------------------------------------------ tunnel

    private fun startTunnel(fallback: Fallback, mtu: Int) {
        val config = engine.currentConfig() ?: return refreshConfig()
        scope.launch {
            try {
                val tun = tunInterface ?: establishTun(config, mtu).also { tunInterface = it }
                backend.start(config, fallback, mtu, tun)
                dispatch(TunnelEvent.TunnelEstablished)
            } catch (e: TunnelRejectedException) {
                dispatch(TunnelEvent.TunnelFailed(FailureReason.PEER_REJECTED))
            } catch (e: Exception) {
                Log.w(TAG, "tunnel start failed: ${e.message}")
                dispatch(TunnelEvent.TunnelFailed(FailureReason.HANDSHAKE_TIMEOUT))
            }
        }
    }

    /**
     * Builds the tun interface.
     *
     * The MTU comes from the engine rather than the config: it is per-network
     * and learned, and it is the difference between "connects but nothing
     * loads" and a working connection.
     */
    private fun establishTun(config: TunnelConfig, mtu: Int): ParcelFileDescriptor {
        val builder = Builder()
            .setSession(config.server.displayName)
            .setMtu(mtu)
            .setBlocking(true)
            .setConfigureIntent(openAppIntent())

        val (address, prefix) = config.address.split("/").let {
            it[0] to (it.getOrNull(1)?.toIntOrNull() ?: 32)
        }
        builder.addAddress(address, prefix)
        config.dns.forEach(builder::addDnsServer)
        config.allowedIps.forEach { cidr ->
            val parts = cidr.split("/")
            // ::/0 is included even though we hand out no IPv6 address: it
            // captures IPv6 traffic into a tunnel that cannot carry it, which
            // drops it. Without that route, IPv6-capable networks would leak
            // around the VPN entirely.
            runCatching { builder.addRoute(parts[0], parts.getOrNull(1)?.toIntOrNull() ?: 0) }
        }
        // Never route our own traffic into the tunnel: if the tunnel is down,
        // the API call that would fix it must still be able to get out.
        runCatching { builder.addDisallowedApplication(packageName) }

        return builder.establish() ?: throw IllegalStateException("VpnService.establish() returned null")
    }

    /**
     * Applies the kill-switch decision.
     *
     *  - [TrafficPolicy.TUNNEL]: tun up, backend up.
     *  - [TrafficPolicy.BLOCK]: tun up, backend down — packets enter the tun and
     *    go nowhere. Nothing leaks.
     *  - [TrafficPolicy.DIRECT]: tun torn down, traffic takes the normal route.
     *    Soft mode only, and the notification says so.
     */
    private fun applyTrafficPolicy(policy: TrafficPolicy) {
        when (policy) {
            TrafficPolicy.TUNNEL -> Unit
            TrafficPolicy.BLOCK -> backend.stop()
            TrafficPolicy.DIRECT -> {
                backend.stop()
                closeTun()
            }
        }
    }

    private fun closeTun() {
        runCatching { tunInterface?.close() }
        tunInterface = null
    }

    // ------------------------------------------------------------------ timers

    private fun scheduleRetry(delayMillis: Long) {
        retryJob?.cancel()
        retryJob = scope.launch {
            delay(delayMillis)
            dispatch(TunnelEvent.RetryTimerFired)
        }
    }

    private fun scheduleGrace(delayMillis: Long) {
        graceJob?.cancel()
        graceJob = scope.launch {
            delay(delayMillis)
            dispatch(TunnelEvent.GraceWindowExpired)
        }
    }

    private fun scheduleStability(delayMillis: Long) {
        stabilityJob?.cancel()
        stabilityJob = scope.launch {
            delay(delayMillis)
            dispatch(TunnelEvent.StabilityTimerFired)
        }
    }

    private fun cancelTimers() {
        retryJob?.cancel()
        graceJob?.cancel()
        stabilityJob?.cancel()
    }

    private fun refreshConfig() {
        scope.launch {
            when (val result = repository.provision()) {
                is ProvisionResult.Success -> {
                    engine.updateConfig(result.config)
                    dispatch(TunnelEvent.RetryTimerFired)
                }

                ProvisionResult.NotEntitled ->
                    dispatch(TunnelEvent.TunnelFailed(FailureReason.SUBSCRIPTION_EXPIRED))

                is ProvisionResult.Unavailable -> {
                    // The control plane has recorded what we need and is
                    // retrying on its side; we just wait rather than hammering.
                    delay(result.retryAfterSeconds * 1000L)
                    dispatch(TunnelEvent.RetryTimerFired)
                }

                ProvisionResult.Unauthorized ->
                    dispatch(TunnelEvent.TunnelFailed(FailureReason.CONFIG_INVALID))
            }
        }
    }

    private fun disconnectIntent(): PendingIntent =
        PendingIntent.getService(
            this, 0,
            Intent(this, ProxifyVpnService::class.java).setAction(ACTION_DISCONNECT),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

    private fun openAppIntent(): PendingIntent =
        PendingIntent.getActivity(
            this, 1,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

    companion object {
        private const val TAG = "ProxifyVpn"
        const val ACTION_CONNECT = "ng.proxify.vpn.CONNECT"
        const val ACTION_DISCONNECT = "ng.proxify.vpn.DISCONNECT"

        /**
         * The running service, for the UI to observe. Held weakly in spirit: it
         * is cleared in onDestroy, and every reader must handle null because
         * the OS can kill this service at any moment — which on our target
         * devices it will.
         */
        @Volatile
        var instance: ProxifyVpnService? = null
            private set

        fun connect(context: android.content.Context) {
            context.startForegroundService(
                Intent(context, ProxifyVpnService::class.java).setAction(ACTION_CONNECT),
            )
        }

        fun disconnect(context: android.content.Context) {
            context.startService(
                Intent(context, ProxifyVpnService::class.java).setAction(ACTION_DISCONNECT),
            )
        }

        fun isConnected(): Boolean = instance?.state?.value?.status == ConnectionStatus.CONNECTED
    }
}

/** The edge does not know our key. Re-provision rather than retry. */
class TunnelRejectedException(message: String) : Exception(message)

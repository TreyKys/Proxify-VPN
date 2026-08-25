package ng.proxify.vpn.vpn

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import ng.proxify.core.KillSwitchMode
import ng.proxify.core.TunnelConfig
import ng.proxify.vpn.data.ApiClient
import ng.proxify.vpn.data.ApiException
import ng.proxify.vpn.data.DeviceKeys
import ng.proxify.vpn.data.SessionStore

/** The outcome of asking the control plane for a tunnel. */
sealed interface ProvisionResult {
    data class Success(val config: TunnelConfig) : ProvisionResult

    /** No active pass. The app shows the paywall; the engine stops retrying. */
    data object NotEntitled : ProvisionResult

    /**
     * The control plane could not reach an edge server. It has recorded what we
     * need and its reconciler is already retrying, so we wait rather than
     * hammering — the fix is in progress on their side.
     */
    data class Unavailable(val retryAfterSeconds: Int) : ProvisionResult

    data object Unauthorized : ProvisionResult
}

/**
 * Gets a tunnel config, and caches the last good one.
 *
 * The cache matters more than it looks: it means an app that is killed and
 * restarted — which on these devices happens constantly — can re-establish the
 * tunnel without waiting on a round trip over the same bad network that is
 * probably why it needs to reconnect.
 */
class TunnelRepository(
    private val api: ApiClient,
    private val session: SessionStore,
    private val keys: DeviceKeys,
    private val configCache: TunnelConfigCache,
    private val appVersion: String,
) {
    fun cachedConfig(): TunnelConfig? = configCache.load()

    fun killSwitchMode(): KillSwitchMode = session.killSwitchMode()

    /** Apps the user forced out of the tunnel; see [SessionStore]. */
    fun userBypassedPackages(): Set<String> = session.userBypassedPackages()

    suspend fun provision(): ProvisionResult = withContext(Dispatchers.IO) {
        try {
            // Registering the device is idempotent on the server, so doing it
            // whenever we lack a device id costs nothing and repairs the state
            // of an app whose local data was cleared.
            val deviceId = keys.deviceId() ?: run {
                val device = api.registerDevice(
                    publicKey = keys.publicKey(),
                    name = android.os.Build.MODEL ?: "Android device",
                    appVersion = appVersion,
                )
                keys.setDeviceId(device.id)
                device.id
            }

            val response = api.provision(deviceId, session.preferredServer())
            val config = response.toDomain()
            configCache.save(config)
            ProvisionResult.Success(config)
        } catch (e: ApiException) {
            when {
                e.status == 402 -> ProvisionResult.NotEntitled
                e.status == 401 -> ProvisionResult.Unauthorized
                e.status == 503 -> ProvisionResult.Unavailable(e.retryAfterSeconds.coerceIn(2, 60))
                // A network error reaching the control plane is not a
                // provisioning failure; the config we already hold may well
                // still work, so we retry rather than tearing anything down.
                else -> ProvisionResult.Unavailable(retryAfterSeconds = 5)
            }
        }
    }

    /**
     * Replaces the device keypair and re-registers it. The tunnel must be
     * re-provisioned afterwards, which the caller triggers.
     */
    suspend fun rotateKeys(): ProvisionResult = withContext(Dispatchers.IO) {
        keys.rotate()
        keys.setDeviceId("")
        configCache.clear()
        provision()
    }
}

package ng.proxify.vpn

import android.app.Application
import ng.proxify.vpn.data.ApiClient
import ng.proxify.vpn.data.DeviceKeys
import ng.proxify.vpn.data.SessionStore
import ng.proxify.vpn.vpn.TunnelConfigCache
import ng.proxify.vpn.vpn.TunnelRepository
import ng.proxify.vpn.vpn.WireGuardBackend
import ng.proxify.vpn.vpn.WireGuardGoBackend

/**
 * Manual dependency wiring.
 *
 * No DI framework: this is a small app whose whole selling point is running well
 * on a 2GB phone, and a dependency graph we can read in one screen costs nothing
 * at startup.
 */
class ProxifyApplication : Application() {

    lateinit var session: SessionStore
        private set

    lateinit var deviceKeys: DeviceKeys
        private set

    lateinit var api: ApiClient
        private set

    lateinit var tunnelRepository: TunnelRepository
        private set

    lateinit var wireGuardBackend: WireGuardBackend
        private set

    override fun onCreate() {
        super.onCreate()
        session = SessionStore(this)
        deviceKeys = DeviceKeys(this)
        api = ApiClient(BuildConfig.API_BASE_URL, session)
        wireGuardBackend = WireGuardGoBackend(this, deviceKeys)
        tunnelRepository = TunnelRepository(
            api = api,
            session = session,
            keys = deviceKeys,
            configCache = TunnelConfigCache(this),
            appVersion = BuildConfig.VERSION_NAME,
        )
    }
}

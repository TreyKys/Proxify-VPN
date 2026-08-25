package ng.proxify.vpn.data

import android.content.Context
import ng.proxify.core.KillSwitchMode

/** Session tokens and the handful of user preferences the app keeps. */
class SessionStore(context: Context) {

    private val storage = SecureStorage(context, PREFS_NAME)

    fun accessToken(): String? = storage.getString(KEY_ACCESS)

    fun refreshToken(): String? = storage.getString(KEY_REFRESH)

    fun userId(): String? = storage.getString(KEY_USER)

    fun isSignedIn(): Boolean = refreshToken() != null

    fun save(tokens: TokenResponse) {
        storage.putString(KEY_ACCESS, tokens.accessToken)
        storage.putString(KEY_REFRESH, tokens.refreshToken)
        storage.putString(KEY_USER, tokens.userId)
    }

    fun signOut() {
        storage.remove(KEY_ACCESS)
        storage.remove(KEY_REFRESH)
        storage.remove(KEY_USER)
    }

    /**
     * Soft by default. The default is a product decision, not an oversight:
     * a hard kill switch is what makes incumbent VPNs unusable on flaky
     * networks, so ours is opt-in and clearly explained.
     */
    fun killSwitchMode(): KillSwitchMode =
        if (storage.getString(KEY_KILL_SWITCH) == "strict") KillSwitchMode.STRICT else KillSwitchMode.SOFT

    fun setKillSwitchMode(mode: KillSwitchMode) {
        storage.putString(KEY_KILL_SWITCH, if (mode == KillSwitchMode.STRICT) "strict" else "soft")
    }

    fun preferredServer(): String? = storage.getString(KEY_SERVER)

    fun setPreferredServer(code: String?) {
        if (code == null) storage.remove(KEY_SERVER) else storage.putString(KEY_SERVER, code)
    }

    /**
     * Apps the user has personally forced out of the tunnel.
     *
     * The catalog is a curated guess and will be wrong somewhere — a bank we
     * missed, a package name that changed. This is the escape hatch that turns
     * "this app is broken on Proxify" into a one-tap fix instead of a support
     * ticket, and it is why an incomplete catalog is survivable.
     */
    fun userBypassedPackages(): Set<String> =
        storage.getString(KEY_BYPASS)?.split(",")?.filter { it.isNotBlank() }?.toSet() ?: emptySet()

    fun setAppBypassed(packageName: String, bypassed: Boolean) {
        val current = userBypassedPackages().toMutableSet()
        if (bypassed) current += packageName else current -= packageName
        storage.putString(KEY_BYPASS, current.joinToString(","))
    }

    /** Whether the user has been shown the battery-optimisation guidance. */
    fun batteryGuidanceShown(): Boolean = storage.getString(KEY_BATTERY) == "1"

    fun markBatteryGuidanceShown() = storage.putString(KEY_BATTERY, "1")

    /** Whether the user asked for the tunnel to be on, so we restore it after a reboot. */
    fun tunnelWanted(): Boolean = storage.getString(KEY_WANTED) == "1"

    fun setTunnelWanted(wanted: Boolean) = storage.putString(KEY_WANTED, if (wanted) "1" else "0")

    private companion object {
        const val PREFS_NAME = "proxify_session"
        const val KEY_ACCESS = "access_token"
        const val KEY_REFRESH = "refresh_token"
        const val KEY_USER = "user_id"
        const val KEY_KILL_SWITCH = "kill_switch"
        const val KEY_SERVER = "preferred_server"
        const val KEY_BATTERY = "battery_guidance"
        const val KEY_WANTED = "tunnel_wanted"
        const val KEY_BYPASS = "bypassed_apps"
    }
}

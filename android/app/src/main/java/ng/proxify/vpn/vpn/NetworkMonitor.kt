package ng.proxify.vpn.vpn

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import ng.proxify.core.NetworkSignature
import ng.proxify.core.TunnelEvent

/**
 * Turns Android connectivity callbacks into engine events.
 *
 * The subtlety worth knowing: Android will keep a WiFi network marked
 * "available" while the user walks out of range, and the tunnel dies long before
 * the system admits anything changed. So we do not wait for `onLost` to decide a
 * handoff happened — we compare the *identity* of the active network and treat
 * any change as a handoff.
 *
 * Network identity uses the framework's network handle, never the SSID. Reading
 * SSIDs needs location permission, and asking a VPN user for their location to
 * "improve reliability" is the kind of thing that makes people uninstall.
 */
class NetworkMonitor(
    context: Context,
    private val onEvent: (TunnelEvent) -> Unit,
) {
    private val connectivity =
        context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager

    private var registered = false
    private var current: NetworkSignature? = null

    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            emitFor(network)
        }

        override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) {
            // Fires constantly. The engine ignores a repeat of the same
            // signature, so this is cheap and it catches the case where a
            // network becomes validated only after it becomes available.
            if (capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) {
                emitFor(network, capabilities)
            }
        }

        override fun onLost(network: Network) {
            val active = connectivity.activeNetwork
            if (active == null) {
                current = null
                onEvent(TunnelEvent.NetworkLost)
            } else {
                // Losing one network while another is live is a handoff, and
                // handling it as such is what keeps the tunnel alive when a user
                // walks out of WiFi range onto mobile data.
                emitFor(active)
            }
        }
    }

    fun start() {
        if (registered) return
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
        connectivity.registerNetworkCallback(request, callback)
        registered = true

        val active = connectivity.activeNetwork
        if (active != null) emitFor(active) else onEvent(TunnelEvent.NetworkLost)
    }

    fun stop() {
        if (!registered) return
        runCatching { connectivity.unregisterNetworkCallback(callback) }
        registered = false
    }

    private fun emitFor(
        network: Network,
        capabilities: NetworkCapabilities? = connectivity.getNetworkCapabilities(network),
    ) {
        val signature = signatureOf(network, capabilities) ?: return
        if (signature == current) return
        current = signature
        onEvent(TunnelEvent.NetworkAvailable(signature))
    }

    private fun signatureOf(
        network: Network,
        capabilities: NetworkCapabilities?,
    ): NetworkSignature? {
        val caps = capabilities ?: return null
        val kind = when {
            caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> NetworkSignature.Kind.WIFI
            caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> NetworkSignature.Kind.CELLULAR
            caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> NetworkSignature.Kind.ETHERNET
            else -> NetworkSignature.Kind.OTHER
        }
        // networkHandle is stable for the lifetime of a network and changes when
        // the phone moves to a different one — exactly the handoff signal we
        // want, with no permissions and nothing identifying stored.
        return NetworkSignature(kind, network.networkHandle.toString())
    }
}

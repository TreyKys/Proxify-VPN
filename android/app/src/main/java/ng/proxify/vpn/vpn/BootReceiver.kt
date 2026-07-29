package ng.proxify.vpn.vpn

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.VpnService
import ng.proxify.vpn.ProxifyApplication

/**
 * Restores the tunnel after a reboot or an app update.
 *
 * Only if the user had it on. A VPN that turns itself on unasked is a VPN
 * people uninstall — and [VpnService.prepare] would refuse anyway unless
 * consent was already granted, which is the check below.
 */
class BootReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED -> Unit
            else -> return
        }

        val app = context.applicationContext as ProxifyApplication
        if (!app.session.tunnelWanted() || !app.session.isSignedIn()) return

        // A null return means consent is still granted; anything else means the
        // user must approve in the app first, so we do nothing.
        if (VpnService.prepare(context) != null) return

        ProxifyVpnService.connect(context)
    }
}

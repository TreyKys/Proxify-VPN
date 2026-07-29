package ng.proxify.vpn.vpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import androidx.core.app.NotificationCompat
import ng.proxify.core.ConnectionState
import ng.proxify.core.ConnectionStatus
import ng.proxify.vpn.R

/**
 * The persistent notification.
 *
 * It is not decoration — it is both the thing that keeps the OS from killing
 * the service, and the only honest channel we have for telling the user their
 * traffic is currently unprotected. The wording below is deliberately plain: a
 * user who is leaking traffic must be able to tell at a glance, without knowing
 * what a tunnel is.
 */
class TunnelNotifications(private val context: Context) {

    private val manager =
        context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

    init {
        val channel = NotificationChannel(
            CHANNEL_ID,
            context.getString(R.string.notification_channel_name),
            // Low importance: persistent, silent, no interruption. A VPN that
            // buzzes on every reconnect gets its notifications turned off, and
            // on these devices that means the service gets killed.
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = context.getString(R.string.notification_channel_description)
            setShowBadge(false)
        }
        manager.createNotificationChannel(channel)
    }

    fun build(
        state: ConnectionState,
        disconnectIntent: PendingIntent,
        openIntent: PendingIntent,
    ): Notification {
        val (title, text) = wording(state)
        return NotificationCompat.Builder(context, CHANNEL_ID)
            .setSmallIcon(iconFor(state.status))
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(openIntent)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setShowWhen(false)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .addAction(
                R.drawable.ic_disconnect,
                context.getString(R.string.action_disconnect),
                disconnectIntent,
            )
            .build()
    }

    fun update(state: ConnectionState, disconnectIntent: PendingIntent, openIntent: PendingIntent) {
        manager.notify(NOTIFICATION_ID, build(state, disconnectIntent, openIntent))
    }

    private fun wording(state: ConnectionState): Pair<String, String> {
        val server = state.serverCode ?: context.getString(R.string.server_unknown)
        return when (state.status) {
            ConnectionStatus.CONNECTED ->
                context.getString(R.string.status_connected) to
                    context.getString(R.string.status_connected_detail, server)

            ConnectionStatus.CONNECTING ->
                context.getString(R.string.status_connecting) to
                    context.getString(R.string.status_connecting_detail)

            ConnectionStatus.RECONNECTING ->
                context.getString(R.string.status_reconnecting) to
                    context.getString(R.string.status_reconnecting_detail)

            // The one notification that must never be soft-pedalled.
            ConnectionStatus.UNPROTECTED ->
                context.getString(R.string.status_unprotected) to
                    context.getString(R.string.status_unprotected_detail)

            ConnectionStatus.NO_NETWORK ->
                context.getString(R.string.status_no_network) to
                    context.getString(R.string.status_no_network_detail)

            ConnectionStatus.FAILED ->
                context.getString(R.string.status_failed) to
                    context.getString(R.string.status_failed_detail)

            ConnectionStatus.DISCONNECTED ->
                context.getString(R.string.status_disconnected) to
                    context.getString(R.string.status_disconnected_detail)
        }
    }

    private fun iconFor(status: ConnectionStatus): Int = when (status) {
        ConnectionStatus.CONNECTED -> R.drawable.ic_shield_on
        ConnectionStatus.UNPROTECTED, ConnectionStatus.FAILED -> R.drawable.ic_shield_off
        else -> R.drawable.ic_shield_pending
    }

    companion object {
        const val CHANNEL_ID = "proxify_tunnel"
        const val NOTIFICATION_ID = 1001
    }
}

package ng.proxify.vpn.ui

import android.content.Intent
import android.net.Uri
import android.net.VpnService
import android.os.Bundle
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import ng.proxify.vpn.ProxifyApplication
import ng.proxify.vpn.vpn.ProxifyVpnService

class MainActivity : ComponentActivity() {

    private val viewModel: MainViewModel by viewModels {
        object : ViewModelProvider.Factory {
            @Suppress("UNCHECKED_CAST")
            override fun <T : androidx.lifecycle.ViewModel> create(modelClass: Class<T>): T =
                MainViewModel(application as ProxifyApplication) as T
        }
    }

    /** VpnService consent. Android requires it once, from an Activity. */
    private val vpnConsent = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == RESULT_OK) startTunnel() else viewModel.markTunnelWanted(false)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        observeTunnel()

        setContent {
            ProxifyTheme {
                val state by viewModel.state.collectAsState()
                ProxifyApp(
                    state = state,
                    onSignIn = viewModel::signIn,
                    onSignOut = viewModel::signOut,
                    onConnect = ::requestConnect,
                    onDisconnect = ::stopTunnel,
                    onSelectServer = viewModel::selectServer,
                    onKillSwitchModeChange = viewModel::setKillSwitchMode,
                    onBuy = viewModel::buy,
                    onOpenBatterySettings = ::openBatterySettings,
                    onDismissBatteryGuidance = viewModel::dismissBatteryGuidance,
                    onDismissError = viewModel::dismissError,
                    onCheckout = ::openCheckout,
                )
            }
        }
    }

    override fun onResume() {
        super.onResume()
        viewModel.refresh()
        // If the user just came back from Paystack, settle it now rather than
        // waiting for a webhook they cannot see.
        viewModel.confirmPayment()
    }

    /**
     * Mirrors the service's state into the UI. The service can be killed and
     * restarted by the OS at any time, so this re-subscribes rather than
     * holding a reference.
     */
    private fun observeTunnel() {
        lifecycleScope.launch {
            ProxifyVpnService.instance?.state?.collectLatest(viewModel::onConnectionState)
        }
    }

    private fun requestConnect() {
        viewModel.markTunnelWanted(true)
        val consentIntent = VpnService.prepare(this)
        if (consentIntent != null) vpnConsent.launch(consentIntent) else startTunnel()
    }

    private fun startTunnel() {
        ProxifyVpnService.connect(this)
        observeTunnel()
    }

    private fun stopTunnel() {
        viewModel.markTunnelWanted(false)
        ProxifyVpnService.disconnect(this)
    }

    /**
     * Sends the user to the battery-optimisation screen.
     *
     * We deliberately do not request `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`:
     * the permission draws Play Store review scrutiny, and the settings screen
     * achieves the same outcome with the user making the choice themselves.
     */
    private fun openBatterySettings() {
        val intent = Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS)
        runCatching { startActivity(intent) }
            .onFailure { startActivity(Intent(Settings.ACTION_SETTINGS)) }
        viewModel.dismissBatteryGuidance()
    }

    private fun openCheckout(url: String) {
        viewModel.checkoutOpened()
        runCatching { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) }
    }
}

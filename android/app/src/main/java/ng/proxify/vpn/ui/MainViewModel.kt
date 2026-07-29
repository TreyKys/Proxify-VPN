package ng.proxify.vpn.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import ng.proxify.core.ConnectionState
import ng.proxify.core.KillSwitchMode
import ng.proxify.vpn.ProxifyApplication
import ng.proxify.vpn.data.ApiException
import ng.proxify.vpn.data.PlanResponse
import ng.proxify.vpn.data.ServerResponse

data class UiState(
    val signedIn: Boolean = false,
    val connection: ConnectionState = ConnectionState.disconnected,
    val servers: List<ServerResponse> = emptyList(),
    val plans: List<PlanResponse> = emptyList(),
    val preferredServer: String? = null,
    val subscriptionActive: Boolean = false,
    val subscriptionExpiresAt: String = "",
    val killSwitchMode: KillSwitchMode = KillSwitchMode.SOFT,
    val showBatteryGuidance: Boolean = false,
    val busy: Boolean = false,
    val error: String? = null,
    val checkoutUrl: String? = null,
)

class MainViewModel(private val app: ProxifyApplication) : ViewModel() {

    private val _state = MutableStateFlow(
        UiState(
            signedIn = app.session.isSignedIn(),
            preferredServer = app.session.preferredServer(),
            killSwitchMode = app.session.killSwitchMode(),
        ),
    )
    val state: StateFlow<UiState> = _state.asStateFlow()

    fun refresh() {
        viewModelScope.launch {
            runCatching {
                withContext(Dispatchers.IO) {
                    val servers = app.api.servers()
                    val plans = app.api.plans()
                    val account = if (app.session.isSignedIn()) app.api.me() else null
                    Triple(servers, plans, account)
                }
            }.onSuccess { (servers, plans, account) ->
                _state.update {
                    it.copy(
                        servers = servers,
                        plans = plans,
                        subscriptionActive = account?.subscription?.active ?: false,
                        subscriptionExpiresAt = account?.subscription?.expiresAt.orEmpty(),
                    )
                }
            }.onFailure { e -> showError(e) }
        }
    }

    fun signIn(identifier: String, password: String, isSignup: Boolean) {
        viewModelScope.launch {
            _state.update { it.copy(busy = true, error = null) }
            runCatching {
                withContext(Dispatchers.IO) {
                    if (isSignup) app.api.signup(identifier, password)
                    else app.api.login(identifier, password)
                }
            }.onSuccess {
                _state.update {
                    it.copy(
                        signedIn = true,
                        busy = false,
                        // Prompt for the battery-optimisation exemption right
                        // after sign-in. On these devices, skipping it means the
                        // tunnel dies whenever the screen has been off a while,
                        // and the user blames us.
                        showBatteryGuidance = !app.session.batteryGuidanceShown(),
                    )
                }
                refresh()
            }.onFailure { e ->
                _state.update { it.copy(busy = false) }
                showError(e)
            }
        }
    }

    fun signOut() {
        app.session.signOut()
        app.session.setTunnelWanted(false)
        _state.update { UiState(signedIn = false) }
    }

    fun onConnectionState(state: ConnectionState) {
        _state.update { it.copy(connection = state) }
    }

    fun selectServer(code: String?) {
        app.session.setPreferredServer(code)
        _state.update { it.copy(preferredServer = code) }
    }

    fun setKillSwitchMode(mode: KillSwitchMode) {
        app.session.setKillSwitchMode(mode)
        _state.update { it.copy(killSwitchMode = mode) }
    }

    fun markTunnelWanted(wanted: Boolean) = app.session.setTunnelWanted(wanted)

    fun dismissBatteryGuidance() {
        app.session.markBatteryGuidanceShown()
        _state.update { it.copy(showBatteryGuidance = false) }
    }

    fun buy(planCode: String) {
        viewModelScope.launch {
            _state.update { it.copy(busy = true, error = null) }
            runCatching { withContext(Dispatchers.IO) { app.api.initializePayment(planCode) } }
                .onSuccess { init ->
                    pendingReference = init.reference
                    _state.update { it.copy(busy = false, checkoutUrl = init.authorizationUrl) }
                }
                .onFailure { e ->
                    _state.update { it.copy(busy = false) }
                    showError(e)
                }
        }
    }

    fun checkoutOpened() = _state.update { it.copy(checkoutUrl = null) }

    /**
     * Called when the user comes back from the payment page. We ask the server
     * to verify with Paystack rather than believing the browser — and this is
     * also the path that rescues a user whose webhook has not landed yet.
     */
    fun confirmPayment() {
        val reference = pendingReference ?: return
        viewModelScope.launch {
            _state.update { it.copy(busy = true) }
            runCatching { withContext(Dispatchers.IO) { app.api.verifyPayment(reference) } }
                .onSuccess { result ->
                    if (result.active) pendingReference = null
                    _state.update { it.copy(busy = false, subscriptionActive = result.active) }
                    refresh()
                }
                .onFailure { e ->
                    _state.update { it.copy(busy = false) }
                    showError(e)
                }
        }
    }

    fun dismissError() = _state.update { it.copy(error = null) }

    private var pendingReference: String? = null

    private fun showError(e: Throwable) {
        val message = when {
            e is ApiException && e.status == 0 ->
                "Can't reach Proxify. Check your connection and try again."

            e is ApiException && e.code == "invalid_credentials" ->
                "Wrong phone/email or password."

            e is ApiException && e.code == "account_exists" ->
                "You already have an account. Log in instead."

            e is ApiException && e.code == "rate_limited" ->
                "Too many attempts. Wait a minute and try again."

            e is ApiException && e.message.isNotBlank() -> e.message
            else -> "Something went wrong. Try again."
        }
        _state.update { it.copy(error = message) }
    }
}

package ng.proxify.vpn.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import ng.proxify.core.ConnectionStatus
import ng.proxify.core.KillSwitchMode

@Composable
fun ProxifyApp(
    state: UiState,
    onSignIn: (String, String, Boolean) -> Unit,
    onSignOut: () -> Unit,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
    onSelectServer: (String?) -> Unit,
    onKillSwitchModeChange: (KillSwitchMode) -> Unit,
    onBuy: (String) -> Unit,
    onOpenBatterySettings: () -> Unit,
    onDismissBatteryGuidance: () -> Unit,
    onDismissError: () -> Unit,
    onCheckout: (String) -> Unit,
) {
    state.checkoutUrl?.let(onCheckout)

    Scaffold { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(20.dp),
        ) {
            if (!state.signedIn) {
                AuthScreen(state, onSignIn)
            } else {
                ConnectScreen(
                    state = state,
                    onConnect = onConnect,
                    onDisconnect = onDisconnect,
                    onSelectServer = onSelectServer,
                    onKillSwitchModeChange = onKillSwitchModeChange,
                    onBuy = onBuy,
                    onSignOut = onSignOut,
                )
            }
        }
    }

    if (state.showBatteryGuidance) {
        BatteryGuidanceDialog(onOpenBatterySettings, onDismissBatteryGuidance)
    }
    state.error?.let { message ->
        AlertDialog(
            onDismissRequest = onDismissError,
            confirmButton = { TextButton(onClick = onDismissError) { Text("OK") } },
            title = { Text("Something went wrong") },
            text = { Text(message) },
        )
    }
}

@Composable
private fun AuthScreen(state: UiState, onSignIn: (String, String, Boolean) -> Unit) {
    var identifier by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var isSignup by remember { mutableStateOf(true) }

    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("Proxify", style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.Bold)
        Text(
            // The positioning line from the brief. It is a promise about
            // reliability, and deliberately not a promise about speed.
            "The VPN that doesn't drop on Nigerian networks.",
            style = MaterialTheme.typography.bodyMedium,
        )
        Spacer(Modifier.height(12.dp))

        OutlinedTextField(
            value = identifier,
            onValueChange = { identifier = it },
            label = { Text("Phone number or email") },
            placeholder = { Text("0803 123 4567") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(
            value = password,
            onValueChange = { password = it },
            label = { Text("Password") },
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            modifier = Modifier.fillMaxWidth(),
        )

        Button(
            onClick = { onSignIn(identifier, password, isSignup) },
            enabled = !state.busy && identifier.isNotBlank() && password.length >= 8,
            modifier = Modifier.fillMaxWidth(),
        ) {
            if (state.busy) CircularProgressIndicator(Modifier.size(18.dp))
            else Text(if (isSignup) "Create account" else "Log in")
        }
        TextButton(onClick = { isSignup = !isSignup }) {
            Text(if (isSignup) "I already have an account" else "Create an account instead")
        }
        if (isSignup) {
            Text(
                "New accounts include a free data-capped plan, so you can try it before paying.",
                style = MaterialTheme.typography.bodySmall,
            )
        }
    }
}

@Composable
private fun ConnectScreen(
    state: UiState,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
    onSelectServer: (String?) -> Unit,
    onKillSwitchModeChange: (KillSwitchMode) -> Unit,
    onBuy: (String) -> Unit,
    onSignOut: () -> Unit,
) {
    val connection = state.connection

    LazyColumn(verticalArrangement = Arrangement.spacedBy(16.dp)) {
        item { StatusHeader(state) }

        item {
            val connected = connection.status != ConnectionStatus.DISCONNECTED &&
                connection.status != ConnectionStatus.FAILED
            Button(
                onClick = if (connected) onDisconnect else onConnect,
                modifier = Modifier.fillMaxWidth().height(56.dp),
            ) {
                Text(if (connected) "Disconnect" else "Connect")
            }
        }

        if (!state.subscriptionActive) {
            item {
                Card(Modifier.fillMaxWidth()) {
                    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text("Buy a pass", fontWeight = FontWeight.Bold)
                        state.plans.filterNot { it.isFree }.forEach { plan ->
                            Row(
                                Modifier.fillMaxWidth(),
                                horizontalArrangement = Arrangement.SpaceBetween,
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Text("${plan.name} · ₦${plan.priceNaira}")
                                OutlinedButton(onClick = { onBuy(plan.code) }, enabled = !state.busy) {
                                    Text("Buy")
                                }
                            }
                        }
                        Text(
                            "Pay with card, bank transfer or USSD. Passes are prepaid — nothing recurring, nothing to cancel.",
                            style = MaterialTheme.typography.bodySmall,
                        )
                    }
                }
            }
        }

        item { Text("Location", fontWeight = FontWeight.Bold) }
        item {
            ServerRow(
                label = "Automatic (recommended)",
                subtitle = "Picks the closest server that isn't busy",
                selected = state.preferredServer == null,
                onClick = { onSelectServer(null) },
            )
        }
        items(state.servers) { server ->
            ServerRow(
                label = "${server.displayName} (${server.countryCode})",
                subtitle = when (server.load) {
                    "low" -> "Not busy"
                    "medium" -> "Moderately busy"
                    "high" -> "Busy"
                    else -> ""
                },
                selected = state.preferredServer == server.code,
                onClick = { onSelectServer(server.code) },
            )
        }

        item {
            Card(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Row(
                        Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text("Strict mode", fontWeight = FontWeight.Bold)
                        Switch(
                            checked = state.killSwitchMode == KillSwitchMode.STRICT,
                            onCheckedChange = {
                                onKillSwitchModeChange(if (it) KillSwitchMode.STRICT else KillSwitchMode.SOFT)
                            },
                        )
                    }
                    // The honest description of the tradeoff, in both directions.
                    Text(
                        if (state.killSwitchMode == KillSwitchMode.STRICT) {
                            "Nothing leaves your phone unless the tunnel is up. If the connection drops, your internet stops until it comes back."
                        } else {
                            "If the tunnel drops, we hold your traffic for a few seconds while reconnecting. If it takes longer, your internet keeps working but is no longer protected — and we tell you."
                        },
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            }
        }

        item { TextButton(onClick = onSignOut) { Text("Sign out") } }
    }
}

@Composable
private fun StatusHeader(state: UiState) {
    val connection = state.connection
    val (label, detail) = when (connection.status) {
        ConnectionStatus.CONNECTED -> "Protected" to "Connected to ${connection.serverCode ?: "server"}"
        ConnectionStatus.CONNECTING -> "Connecting…" to "Setting up your tunnel"
        ConnectionStatus.RECONNECTING -> "Reconnecting…" to "Holding your traffic while we reconnect"
        // Never dressed up. A user who thinks they are protected when they are
        // not is worse off than a user who knows they are exposed.
        ConnectionStatus.UNPROTECTED -> "Not protected" to "Your internet is working, but traffic is not going through the VPN"
        ConnectionStatus.NO_NETWORK -> "No network" to "Waiting for a connection"
        ConnectionStatus.FAILED -> "Stopped" to "Buy a pass or check your account"
        ConnectionStatus.DISCONNECTED -> "Off" to "Tap connect to protect your traffic"
    }

    Row(verticalAlignment = Alignment.CenterVertically) {
        Spacer(
            Modifier
                .size(14.dp)
                .clip(CircleShape)
                .background(statusColor(connection.status)),
        )
        Spacer(Modifier.size(12.dp))
        Column {
            Text(label, style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
            Text(detail, style = MaterialTheme.typography.bodySmall)
            if (connection.status == ConnectionStatus.RECONNECTING && connection.attempt > 2) {
                Text(
                    "Attempt ${connection.attempt} · trying ${connection.transport.name.lowercase()}",
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }
    }
}

private fun statusColor(status: ConnectionStatus): Color = when (status) {
    ConnectionStatus.CONNECTED -> Color(0xFF1B9C5B)
    ConnectionStatus.CONNECTING, ConnectionStatus.RECONNECTING -> Color(0xFFE0A100)
    ConnectionStatus.UNPROTECTED, ConnectionStatus.FAILED -> Color(0xFFC5372C)
    else -> Color(0xFF8A8A8A)
}

@Composable
private fun ServerRow(label: String, subtitle: String, selected: Boolean, onClick: () -> Unit) {
    Card(Modifier.fillMaxWidth().clickable(onClick = onClick)) {
        Row(
            Modifier.fillMaxWidth().padding(16.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column {
                Text(label, fontWeight = if (selected) FontWeight.Bold else FontWeight.Normal)
                if (subtitle.isNotBlank()) {
                    Text(subtitle, style = MaterialTheme.typography.bodySmall)
                }
            }
            if (selected) Text("✓")
        }
    }
}

@Composable
private fun BatteryGuidanceDialog(onOpenSettings: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Keep Proxify running") },
        text = {
            Text(
                "Android may shut Proxify down when your screen is off, which drops the VPN " +
                    "without warning. Allowing it to run in the background is the single biggest " +
                    "thing you can do to keep your connection stable.",
            )
        },
        confirmButton = { Button(onClick = onOpenSettings) { Text("Open settings") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Not now") } },
    )
}

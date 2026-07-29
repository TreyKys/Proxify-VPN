package ng.proxify.vpn.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

private val Green = Color(0xFF0E7A4B)
private val GreenDark = Color(0xFF6FD1A0)

private val LightColors = lightColorScheme(
    primary = Green,
    secondary = Color(0xFF1F5C8B),
)

private val DarkColors = darkColorScheme(
    primary = GreenDark,
    secondary = Color(0xFF8FC4EE),
)

/**
 * No dynamic colour and no custom fonts: both cost startup time and APK size,
 * and this app has to feel instant on a 2GB phone.
 */
@Composable
fun ProxifyTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (isSystemInDarkTheme()) DarkColors else LightColors,
        content = content,
    )
}

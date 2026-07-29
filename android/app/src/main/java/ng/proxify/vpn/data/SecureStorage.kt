package ng.proxify.vpn.data

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey

/**
 * Keystore-backed preferences for the two things worth protecting: the device's
 * WireGuard private key and the session tokens.
 *
 * If the encrypted store cannot be opened — which happens on a small number of
 * cheap devices with broken Keystore implementations, and our users have
 * exactly those devices — we fall back to ordinary private preferences rather
 * than refusing to run. A VPN that will not start on a ₦40,000 phone protects
 * nobody. The fallback is recorded so the app can tell the user their keys are
 * stored with app-sandbox protection only.
 */
class SecureStorage(context: Context, name: String) {

    var usingKeystore: Boolean = true
        private set

    private val prefs: SharedPreferences = try {
        val masterKey = MasterKey.Builder(context)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        EncryptedSharedPreferences.create(
            context,
            name,
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    } catch (e: Exception) {
        usingKeystore = false
        context.getSharedPreferences("${name}_plain", Context.MODE_PRIVATE)
    }

    fun getString(key: String): String? = prefs.getString(key, null)

    fun putString(key: String, value: String) {
        prefs.edit().putString(key, value).apply()
    }

    fun remove(key: String) {
        prefs.edit().remove(key).apply()
    }

    fun clear() {
        prefs.edit().clear().apply()
    }
}

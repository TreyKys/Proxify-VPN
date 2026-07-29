package ng.proxify.vpn.data

import android.content.Context
import com.wireguard.crypto.KeyPair as WgKeyPair

/**
 * The device's WireGuard keypair.
 *
 * The private key is generated on the phone and never leaves it. That is not a
 * detail — it is what makes the no-logs claim structurally true rather than a
 * promise: we cannot hand over a key we have never had, under commercial
 * pressure or legal order alike.
 *
 * Keys live in [SecureStorage], which is backed by the Android Keystore. They
 * are excluded from backup (`allowBackup=false` in the manifest) so a restored
 * cloud backup cannot resurrect a key on a different phone.
 */
class DeviceKeys(context: Context) {

    private val storage = SecureStorage(context, PREFS_NAME)

    /** The public key to register with the control plane, generating one if needed. */
    fun publicKey(): String = keyPair().publicKeyBase64

    fun privateKey(): String = keyPair().privateKey

    fun deviceId(): String? = storage.getString(KEY_DEVICE_ID)

    fun setDeviceId(id: String) = storage.putString(KEY_DEVICE_ID, id)

    /**
     * Generates a fresh keypair, discarding the old one. The caller must
     * re-register with the control plane afterwards; until it does, the edge
     * still holds the old key and this device cannot connect.
     */
    fun rotate(): String = generateAndStore().publicKeyBase64

    private fun keyPair(): StoredKeyPair {
        val existingPrivate = storage.getString(KEY_PRIVATE)
        val existingPublic = storage.getString(KEY_PUBLIC)
        if (existingPrivate != null && existingPublic != null) {
            return StoredKeyPair(existingPrivate, existingPublic)
        }
        return generateAndStore()
    }

    private fun generateAndStore(): StoredKeyPair {
        // The WireGuard library's own keypair generator: Curve25519 with the
        // required clamping. Hand-rolling this is how you end up with keys that
        // mostly work, which is the worst failure mode for something a user
        // cannot debug.
        val pair = WgKeyPair()
        val stored = StoredKeyPair(
            privateKey = pair.privateKey.toBase64(),
            publicKeyBase64 = pair.publicKey.toBase64(),
        )
        storage.putString(KEY_PRIVATE, stored.privateKey)
        storage.putString(KEY_PUBLIC, stored.publicKeyBase64)
        return stored
    }

    private data class StoredKeyPair(val privateKey: String, val publicKeyBase64: String)

    private companion object {
        const val PREFS_NAME = "proxify_keys"
        const val KEY_PRIVATE = "wg_private"
        const val KEY_PUBLIC = "wg_public"
        const val KEY_DEVICE_ID = "device_id"
    }
}

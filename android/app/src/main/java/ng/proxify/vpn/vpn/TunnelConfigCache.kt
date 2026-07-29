package ng.proxify.vpn.vpn

import android.content.Context
import kotlinx.serialization.json.Json
import ng.proxify.core.TunnelConfig
import ng.proxify.vpn.data.SecureStorage
import ng.proxify.vpn.data.TunnelConfigResponse

/**
 * The last known-good tunnel config.
 *
 * Stored encrypted because it names the server a user connects to, which is the
 * one piece of routing information we hold on the device. It is deleted on sign
 * out and on key rotation.
 */
class TunnelConfigCache(context: Context) {

    private val storage = SecureStorage(context, PREFS_NAME)
    private val json = Json { ignoreUnknownKeys = true; explicitNulls = false }

    fun load(): TunnelConfig? {
        val raw = storage.getString(KEY_CONFIG) ?: return null
        return runCatching {
            json.decodeFromString(TunnelConfigResponse.serializer(), raw).toDomain()
        }.getOrNull()
    }

    fun save(config: TunnelConfig) {
        // Round-tripping through the wire type keeps one definition of the
        // format instead of a second, quietly diverging one for storage.
        val response = config.toWire()
        storage.putString(KEY_CONFIG, json.encodeToString(TunnelConfigResponse.serializer(), response))
    }

    fun clear() = storage.remove(KEY_CONFIG)

    private companion object {
        const val PREFS_NAME = "proxify_tunnel"
        const val KEY_CONFIG = "config"
    }
}

private fun TunnelConfig.toWire(): TunnelConfigResponse = TunnelConfigResponse(
    version = version,
    assignmentId = assignmentId,
    address = address,
    dns = dns,
    mtu = mtu,
    allowedIps = allowedIps,
    serverPublicKey = serverPublicKey,
    endpoint = endpoint.toString(),
    persistentKeepalive = persistentKeepalive,
    fallbacks = fallbacks.map {
        ng.proxify.vpn.data.FallbackResponse(
            transport = it.transport.name.lowercase().replace('_', '-'),
            endpoint = it.endpoint.toString(),
            note = it.note,
        )
    },
    obfuscation = obfuscation,
    server = ng.proxify.vpn.data.ServerResponse(
        code = server.code,
        displayName = server.displayName,
        countryCode = server.countryCode,
        region = server.region,
    ),
    expiresAt = java.time.Instant.ofEpochSecond(expiresAtEpochSeconds).toString(),
)

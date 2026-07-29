package ng.proxify.core

/**
 * The tunnel configuration handed down by the control plane.
 *
 * This mirrors `provision.TunnelConfig` on the server. It is plain Kotlin with
 * no Android or serialization dependencies so the reliability engine — the part
 * of this app that actually has to be correct — can be tested on the JVM.
 */
data class TunnelConfig(
    val version: Int,
    val assignmentId: String,
    val address: String,
    val dns: List<String>,
    val mtu: Int,
    val allowedIps: List<String>,
    val serverPublicKey: String,
    val endpoint: Endpoint,
    val persistentKeepalive: Int,
    val fallbacks: List<Fallback>,
    val obfuscation: Map<String, String> = emptyMap(),
    val server: ServerInfo,
    val expiresAtEpochSeconds: Long,
) {
    /**
     * The ordered list of ways to reach this server, primary first. The engine
     * walks it when a transport turns out to be blocked or throttled.
     */
    val transportLadder: List<Fallback>
        get() = buildList {
            add(Fallback(Transport.UDP, endpoint, "primary"))
            addAll(fallbacks.filter { it.transport != Transport.UDP })
        }
}

data class Endpoint(val host: String, val port: Int) {
    override fun toString(): String = "$host:$port"

    companion object {
        fun parse(value: String): Endpoint {
            val idx = value.lastIndexOf(':')
            require(idx > 0 && idx < value.length - 1) { "malformed endpoint: $value" }
            val port = value.substring(idx + 1).toIntOrNull()
            require(port != null && port in 1..65535) { "malformed endpoint port: $value" }
            return Endpoint(value.substring(0, idx), port)
        }
    }
}

enum class Transport {
    /** Plain WireGuard over UDP. Fastest, and the first thing carriers throttle. */
    UDP,

    /** WireGuard tunnelled over TCP/443 via the edge's Reality endpoint. */
    TCP,

    /** WebSocket over TLS. Last resort: slowest, hardest to fingerprint. */
    WS_TLS,
    ;

    companion object {
        fun fromWire(value: String): Transport = when (value.lowercase()) {
            "udp" -> UDP
            "tcp" -> TCP
            "ws-tls", "wstls", "ws" -> WS_TLS
            else -> UDP
        }
    }
}

data class Fallback(val transport: Transport, val endpoint: Endpoint, val note: String = "")

data class ServerInfo(
    val code: String,
    val displayName: String,
    val countryCode: String,
    val region: String,
)

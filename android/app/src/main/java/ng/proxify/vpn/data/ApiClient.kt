package ng.proxify.vpn.data

import kotlinx.serialization.DeserializationStrategy
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationStrategy
import kotlinx.serialization.json.Json
import ng.proxify.core.Endpoint
import ng.proxify.core.Fallback
import ng.proxify.core.ServerInfo
import ng.proxify.core.Transport
import ng.proxify.core.TunnelConfig
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.time.Instant
import java.util.concurrent.TimeUnit

/**
 * Control-plane client.
 *
 * Calls block, so every one of them must be made off the main thread — the
 * repository wraps them in [kotlinx.coroutines.Dispatchers.IO]. They are not
 * suspend functions because OkHttp's synchronous call is what we want here and
 * dressing it as suspending would hide that it blocks.
 *
 * Timeouts are set for the network our users are actually on: generous enough
 * that a slow 3G handshake isn't treated as a failure, short enough that the app
 * doesn't appear frozen. Retries are the caller's business — the reliability
 * engine already owns backoff, and a second retry policy hidden in here would
 * fight it.
 */
class ApiClient(
    private val baseUrl: String,
    private val session: SessionStore,
) {
    private val json = Json { ignoreUnknownKeys = true; explicitNulls = false }

    private val http = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .writeTimeout(30, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .build()

    private val jsonMedia = "application/json; charset=utf-8".toMediaType()

    // ------------------------------------------------------------------- auth

    fun signup(identifier: String, password: String): TokenResponse =
        postJson(
            "/v1/auth/signup",
            CredentialsRequest(identifier, password),
            CredentialsRequest.serializer(),
            TokenResponse.serializer(),
        ).also(session::save)

    fun login(identifier: String, password: String): TokenResponse =
        postJson(
            "/v1/auth/login",
            CredentialsRequest(identifier, password),
            CredentialsRequest.serializer(),
            TokenResponse.serializer(),
        ).also(session::save)

    // ---------------------------------------------------------------- devices

    fun registerDevice(publicKey: String, name: String, appVersion: String): DeviceResponse =
        postJson(
            "/v1/devices",
            RegisterDeviceRequest(name = name, platform = "android", publicKey = publicKey, appVersion = appVersion),
            RegisterDeviceRequest.serializer(),
            DeviceResponse.serializer(),
            authenticated = true,
        )

    // ----------------------------------------------------------------- tunnel

    fun provision(deviceId: String, serverCode: String?): TunnelConfigResponse =
        postJson(
            "/v1/tunnel/provision",
            ProvisionRequest(deviceId, serverCode),
            ProvisionRequest.serializer(),
            TunnelConfigResponse.serializer(),
            authenticated = true,
        )

    fun servers(): List<ServerResponse> =
        get("/v1/servers", ServerListResponse.serializer()).servers

    fun plans(): List<PlanResponse> =
        get("/v1/plans", PlanListResponse.serializer()).plans

    fun me(): AccountResponse =
        get("/v1/me", AccountResponse.serializer(), authenticated = true)

    // ---------------------------------------------------------------- payments

    fun initializePayment(planCode: String): PaymentInitResponse =
        postJson(
            "/v1/payments/initialize",
            InitializePaymentRequest(planCode),
            InitializePaymentRequest.serializer(),
            PaymentInitResponse.serializer(),
            authenticated = true,
        )

    fun verifyPayment(reference: String): PaymentVerifyResponse =
        postJson(
            "/v1/payments/verify",
            VerifyPaymentRequest(reference),
            VerifyPaymentRequest.serializer(),
            PaymentVerifyResponse.serializer(),
            authenticated = true,
        )

    // ------------------------------------------------------------------ plumbing

    private fun <T> get(
        path: String,
        deserializer: DeserializationStrategy<T>,
        authenticated: Boolean = false,
    ): T = execute(Request.Builder().url(baseUrl + path).get(), deserializer, authenticated)

    private fun <B, T> postJson(
        path: String,
        body: B,
        serializer: SerializationStrategy<B>,
        deserializer: DeserializationStrategy<T>,
        authenticated: Boolean = false,
    ): T {
        val encoded = json.encodeToString(serializer, body)
        val request = Request.Builder().url(baseUrl + path).post(encoded.toRequestBody(jsonMedia))
        return execute(request, deserializer, authenticated)
    }

    private fun <T> execute(
        builder: Request.Builder,
        deserializer: DeserializationStrategy<T>,
        authenticated: Boolean,
        allowRefresh: Boolean = true,
    ): T {
        if (authenticated) {
            val token = session.accessToken() ?: throw ApiException(401, "unauthorized", "not signed in")
            builder.header("Authorization", "Bearer $token")
        }

        val response = try {
            http.newCall(builder.build()).execute()
        } catch (e: IOException) {
            throw ApiException(0, "network", e.message ?: "network error")
        }

        response.use { result ->
            val body = result.body?.string().orEmpty()
            if (result.isSuccessful) return json.decodeFromString(deserializer, body)

            val error = runCatching { json.decodeFromString(ErrorResponse.serializer(), body) }
                .getOrElse { ErrorResponse("http_${result.code}", "request failed") }

            // One transparent refresh on an expired access token. The user is on
            // a phone that may have been asleep for hours; making them log in
            // again because a 30-minute token lapsed would be its own outage.
            if (result.code == 401 && error.error == "token_expired" && allowRefresh && authenticated) {
                refreshBlocking()
                return execute(builder, deserializer, authenticated = true, allowRefresh = false)
            }
            throw ApiException(result.code, error.error, error.message, error.retryAfterSeconds)
        }
    }

    private fun refreshBlocking() {
        val token = session.refreshToken() ?: throw ApiException(401, "unauthorized", "no session")
        val request = Request.Builder()
            .url(baseUrl + "/v1/auth/refresh")
            .post(json.encodeToString(RefreshRequest.serializer(), RefreshRequest(token)).toRequestBody(jsonMedia))
        val result = execute(request, TokenResponse.serializer(), authenticated = false, allowRefresh = false)
        session.save(result)
    }
}

class ApiException(
    val status: Int,
    val code: String,
    override val message: String,
    val retryAfterSeconds: Int = 0,
) : Exception(message)

// ---------------------------------------------------------------------- wire types

@Serializable
data class CredentialsRequest(val identifier: String, val password: String)

@Serializable
data class RefreshRequest(@SerialName("refresh_token") val refreshToken: String)

@Serializable
data class TokenResponse(
    @SerialName("access_token") val accessToken: String,
    @SerialName("refresh_token") val refreshToken: String,
    @SerialName("expires_at") val expiresAt: String,
    @SerialName("user_id") val userId: String,
)

@Serializable
data class RegisterDeviceRequest(
    val name: String,
    val platform: String,
    @SerialName("public_key") val publicKey: String,
    @SerialName("app_version") val appVersion: String,
)

@Serializable
data class DeviceResponse(
    val id: String,
    val name: String,
    @SerialName("public_key") val publicKey: String,
)

@Serializable
data class ProvisionRequest(
    @SerialName("device_id") val deviceId: String,
    @SerialName("server_code") val serverCode: String? = null,
)

@Serializable
data class TunnelConfigResponse(
    val version: Int,
    @SerialName("assignment_id") val assignmentId: String,
    val address: String,
    val dns: List<String>,
    val mtu: Int,
    @SerialName("allowed_ips") val allowedIps: List<String>,
    @SerialName("server_public_key") val serverPublicKey: String,
    val endpoint: String,
    @SerialName("persistent_keepalive") val persistentKeepalive: Int,
    val fallbacks: List<FallbackResponse> = emptyList(),
    val obfuscation: Map<String, String> = emptyMap(),
    val server: ServerResponse,
    @SerialName("expires_at") val expiresAt: String,
) {
    fun toDomain(): TunnelConfig = TunnelConfig(
        version = version,
        assignmentId = assignmentId,
        address = address,
        dns = dns,
        mtu = mtu,
        allowedIps = allowedIps,
        serverPublicKey = serverPublicKey,
        endpoint = Endpoint.parse(endpoint),
        persistentKeepalive = persistentKeepalive,
        fallbacks = fallbacks.map {
            Fallback(Transport.fromWire(it.transport), Endpoint.parse(it.endpoint), it.note)
        },
        obfuscation = obfuscation,
        server = ServerInfo(server.code, server.displayName, server.countryCode, server.region),
        expiresAtEpochSeconds = runCatching { Instant.parse(expiresAt).epochSecond }.getOrDefault(0L),
    )
}

@Serializable
data class FallbackResponse(val transport: String, val endpoint: String, val note: String = "")

@Serializable
data class ServerResponse(
    val code: String,
    @SerialName("display_name") val displayName: String,
    @SerialName("country_code") val countryCode: String,
    val region: String,
    val load: String = "unknown",
)

@Serializable
data class ServerListResponse(val servers: List<ServerResponse>)

@Serializable
data class PlanResponse(
    val code: String,
    val name: String,
    @SerialName("duration_days") val durationDays: Int,
    @SerialName("price_naira") val priceNaira: Long,
    @SerialName("device_limit") val deviceLimit: Int,
    @SerialName("is_free") val isFree: Boolean,
    @SerialName("data_cap_bytes") val dataCapBytes: Long? = null,
)

@Serializable
data class PlanListResponse(val plans: List<PlanResponse>)

@Serializable
data class AccountResponse(val subscription: SubscriptionResponse)

@Serializable
data class SubscriptionResponse(
    val active: Boolean,
    @SerialName("plan_code") val planCode: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

@Serializable
data class InitializePaymentRequest(@SerialName("plan_code") val planCode: String)

@Serializable
data class PaymentInitResponse(
    val reference: String,
    @SerialName("authorization_url") val authorizationUrl: String,
)

@Serializable
data class VerifyPaymentRequest(val reference: String)

@Serializable
data class PaymentVerifyResponse(val status: String, val granted: Boolean, val active: Boolean)

@Serializable
data class ErrorResponse(
    val error: String,
    val message: String = "",
    @SerialName("retry_after_seconds") val retryAfterSeconds: Int = 0,
)

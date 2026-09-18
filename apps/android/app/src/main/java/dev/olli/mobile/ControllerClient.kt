package dev.olli.mobile

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URI
import java.net.URLEncoder
import java.nio.charset.StandardCharsets

class ControllerClient(settings: ConnectionSettings) {
    private val baseUrl = normalizeBaseUrl(settings.baseUrl)
    private val token = settings.token.trim()
    private val json = Json { ignoreUnknownKeys = true; explicitNulls = false }

    init {
        require(token.length >= 24) { "Bearer token must be at least 24 characters." }
    }

    suspend fun info(): ReadyInfo = get("/api/v1/info", ReadyInfo.serializer())
    suspend fun runs(): List<String> = get("/api/v1/runs", ListSerializer(String.serializer()))
    suspend fun events(runId: String, after: Long): List<RunEvent> = get(
        "/api/v1/runs/${encode(runId)}/events?after=$after&limit=1000",
        ListSerializer(RunEvent.serializer()),
    )
    suspend fun gatewayNodes(): List<GatewayNode> = get("/api/v1/gateway/nodes", ListSerializer(GatewayNode.serializer()))
    suspend fun permissions(): List<PermissionPrompt> = get("/api/v1/permissions", ListSerializer(PermissionPrompt.serializer()))
    suspend fun startRun(prompt: String): StartRunResult = post("/api/v1/runs", StartRunRequest.serializer(), StartRunRequest(prompt), StartRunResult.serializer())
    suspend fun cancelRun() { postNoResult("/api/v1/runs/cancel", null, null) }
    suspend fun permission(id: String, allowed: Boolean, always: Boolean = false) {
        postNoResult("/api/v1/permissions/${encode(id)}", PermissionDecision.serializer(), PermissionDecision(allowed, always))
    }
    suspend fun drain(nodeId: String, draining: Boolean) {
        postNoResult("/api/v1/gateway/nodes/${encode(nodeId)}/drain", DrainRequest.serializer(), DrainRequest(draining))
    }

    private suspend fun <T> get(path: String, serializer: KSerializer<T>): T = request("GET", path, null, serializer)
    private suspend fun <B, T> post(path: String, bodySerializer: KSerializer<B>, body: B, resultSerializer: KSerializer<T>): T = request("POST", path, json.encodeToString(bodySerializer, body), resultSerializer)
    private suspend fun <B> postNoResult(path: String, serializer: KSerializer<B>?, body: B?) {
        requestRaw("POST", path, if (serializer != null && body != null) json.encodeToString(serializer, body) else "{}")
    }
    private suspend fun <T> request(method: String, path: String, body: String?, serializer: KSerializer<T>): T = withContext(Dispatchers.IO) {
        val text = requestRaw(method, path, body)
        json.decodeFromString(serializer, text)
    }
    private suspend fun requestRaw(method: String, path: String, body: String?): String = withContext(Dispatchers.IO) {
        val connection = URI(baseUrl + path).toURL().openConnection() as HttpURLConnection
        try {
            connection.requestMethod = method
            connection.connectTimeout = 15_000
            connection.readTimeout = 30_000
            connection.instanceFollowRedirects = false
            connection.setRequestProperty("Accept", "application/json")
            connection.setRequestProperty("Authorization", "Bearer $token")
            if (body != null) {
                connection.doOutput = true
                connection.setRequestProperty("Content-Type", "application/json")
                connection.outputStream.use { it.write(body.toByteArray(StandardCharsets.UTF_8)) }
            }
            val status = connection.responseCode
            val stream = if (status in 200..299) connection.inputStream else connection.errorStream
            val text = stream?.bufferedReader()?.use { it.readText() }.orEmpty()
            if (status !in 200..299) throw IOException("Controller returned HTTP $status${if (text.isBlank()) "" else ": $text"}")
            text
        } finally { connection.disconnect() }
    }

    companion object {
        fun normalizeBaseUrl(value: String): String {
            val uri = URI(value.trim())
            require(uri.scheme == "https" || (BuildConfig.DEBUG && uri.scheme == "http" && (uri.host == "10.0.2.2" || uri.host == "localhost"))) {
                "Use HTTPS. Debug builds allow HTTP only for 10.0.2.2 or localhost."
            }
            require(uri.userInfo == null && uri.query == null && uri.fragment == null) { "Controller URL cannot contain credentials, query, or fragment." }
            require(uri.path.isNullOrBlank() || uri.path == "/") { "Controller URL must not contain a path." }
            return value.trim().removeSuffix("/")
        }
        private fun encode(value: String): String = URLEncoder.encode(value, StandardCharsets.UTF_8).replace("+", "%20")
    }
}

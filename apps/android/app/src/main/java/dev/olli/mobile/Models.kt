package dev.olli.mobile

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

@Serializable
data class RunEvent(
    val sequence: Long,
    val timestamp: String,
    @SerialName("run_id") val runId: String,
    val kind: String,
    @SerialName("graph_id") val graphId: String? = null,
    @SerialName("node_id") val nodeId: String? = null,
    @SerialName("parent_id") val parentId: String? = null,
    val phase: String? = null,
    val role: String? = null,
    val model: String? = null,
    @SerialName("route_node_id") val routeNodeId: String? = null,
    val status: String? = null,
    val message: String? = null,
    @SerialName("tool_name") val toolName: String? = null,
    @SerialName("duration_ms") val durationMs: Long? = null,
    val metadata: Map<String, JsonElement>? = null,
)

@Serializable
data class GatewayNode(
    val id: String,
    val endpoint: String,
    val healthy: Boolean,
    val draining: Boolean,
    val active: Int,
    val limit: Int,
    val models: List<String> = emptyList(),
    val roles: List<String> = emptyList(),
    val latency: Long = 0,
    val failures: Int = 0,
    @SerialName("circuit_open") val circuitOpen: Boolean = false,
    @SerialName("last_error") val lastError: String? = null,
)

@Serializable data class ReadyInfo(
    val workspace: String,
    val model: String,
    val models: List<String>,
    @SerialName("gateway_enabled") val gatewayEnabled: Boolean,
)
@Serializable data class StartRunRequest(val prompt: String, @SerialName("run_id") val runId: String? = null)
@Serializable data class StartRunResult(@SerialName("run_id") val runId: String)
@Serializable data class PermissionDecision(val allowed: Boolean, val always: Boolean = false)
@Serializable data class DrainRequest(val draining: Boolean)

@Serializable
data class PermissionPrompt(
    val id: String,
    @SerialName("run_id") val runId: String,
    @SerialName("tool_name") val toolName: String,
    val args: Map<String, JsonElement> = emptyMap(),
)

data class NodeState(
    val id: String,
    val role: String = "",
    val model: String = "",
    val routeNodeId: String = "",
    val status: String = "pending",
    val phase: String = "",
    val message: String = "",
    val durationMs: Long = 0,
)

data class ConnectionSettings(val baseUrl: String = "", val token: String = "")

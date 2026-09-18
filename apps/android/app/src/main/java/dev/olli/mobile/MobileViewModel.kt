package dev.olli.mobile

import android.app.Application
import android.content.Context
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

class MobileViewModel(application: Application) : AndroidViewModel(application) {
    private val preferences = application.getSharedPreferences("olli-mobile", Context.MODE_PRIVATE)
    private val _state = MutableStateFlow(MobileUiState(settings = loadSettings()))
    val state: StateFlow<MobileUiState> = _state.asStateFlow()
    private var client: ControllerClient? = null
    private var pollJob: Job? = null

    fun updateSettings(baseUrl: String, token: String) {
        _state.value = _state.value.copy(settings = ConnectionSettings(baseUrl, token))
    }

    fun connect() {
        viewModelScope.launch {
            runCatching {
                val settings = _state.value.settings
                val newClient = ControllerClient(settings)
                val info = newClient.info()
                preferences.edit().putString("baseUrl", settings.baseUrl).remove("token").apply()
                client = newClient
                _state.value = _state.value.copy(connected = true, info = info, error = null)
                refreshRuns(); refreshGateway(); startPolling()
            }.onFailure { _state.value = _state.value.copy(connected = false, error = it.message) }
        }
    }

    fun disconnect() { pollJob?.cancel(); pollJob = null; client = null; _state.value = _state.value.copy(connected = false, settings = _state.value.settings.copy(token = ""), permission = null) }
    fun selectTab(tab: MobileTab) { _state.value = _state.value.copy(tab = tab) }
    fun selectNode(nodeId: String?) { _state.value = _state.value.copy(selectedNodeId = nodeId) }

    fun startRun(prompt: String) {
        val api = client ?: return
        viewModelScope.launch {
            runCatching { api.startRun(prompt) }.onSuccess {
                _state.value = _state.value.copy(activeRunId = it.runId, running = true, events = emptyList(), lastSequence = 0, error = null, tab = MobileTab.GRAPH)
            }.onFailure { showError(it) }
        }
    }
    fun cancelRun() { val api = client ?: return; viewModelScope.launch { runCatching { api.cancelRun() }.onFailure(::showError) } }
    fun loadRun(runId: String) {
        val api = client ?: return
        viewModelScope.launch {
            runCatching { api.events(runId, 0) }.onSuccess { events ->
                _state.value = _state.value.copy(activeRunId = runId, running = false, events = events, lastSequence = events.maxOfOrNull { it.sequence } ?: 0, tab = MobileTab.GRAPH)
            }.onFailure(::showError)
        }
    }
    fun decidePermission(allowed: Boolean, always: Boolean = false) {
        val api = client ?: return; val permission = _state.value.permission ?: return
        viewModelScope.launch { runCatching { api.permission(permission.id, allowed, always) }.onSuccess { _state.value = _state.value.copy(permission = null) }.onFailure(::showError) }
    }
    fun setDrain(node: GatewayNode, draining: Boolean) { val api = client ?: return; viewModelScope.launch { runCatching { api.drain(node.id, draining); refreshGateway() }.onFailure(::showError) } }
    fun clearError() { _state.value = _state.value.copy(error = null) }

    private fun startPolling() {
        pollJob?.cancel()
        pollJob = viewModelScope.launch {
            while (isActive) {
                val runId = _state.value.activeRunId
                if (!runId.isNullOrBlank()) pollEvents(runId)
                refreshPermissions()
                if (_state.value.tab == MobileTab.GATEWAY) refreshGateway()
                delay(1_000)
            }
        }
    }
    private suspend fun pollEvents(runId: String) {
        val api = client ?: return
        runCatching { api.events(runId, _state.value.lastSequence) }.onSuccess { incoming ->
            if (incoming.isEmpty()) return@onSuccess
            val merged = (_state.value.events + incoming).distinctBy { it.sequence }.sortedBy { it.sequence }.takeLast(4096)
            val terminal = incoming.lastOrNull { it.kind in setOf("run_completed", "run_failed", "run_cancelled") }
            _state.value = _state.value.copy(events = merged, lastSequence = merged.lastOrNull()?.sequence ?: 0, running = terminal == null && _state.value.running)
        }.onFailure(::showError)
    }
    private suspend fun refreshRuns() { val api = client ?: return; runCatching { api.runs() }.onSuccess { _state.value = _state.value.copy(runs = it.sortedDescending()) }.onFailure(::showError) }
    private suspend fun refreshGateway() { val api = client ?: return; runCatching { api.gatewayNodes() }.onSuccess { _state.value = _state.value.copy(gatewayNodes = it) }.onFailure(::showError) }
    private suspend fun refreshPermissions() { val api = client ?: return; runCatching { api.permissions() }.onSuccess { _state.value = _state.value.copy(permission = it.firstOrNull()) }.onFailure(::showError) }
    private fun showError(error: Throwable) { _state.value = _state.value.copy(error = error.message ?: "Unknown error") }
    private fun loadSettings() = ConnectionSettings(preferences.getString("baseUrl", "") ?: "", "")
}

enum class MobileTab { RUNS, GRAPH, EVENTS, GATEWAY }

data class MobileUiState(
    val settings: ConnectionSettings = ConnectionSettings(),
    val connected: Boolean = false,
    val info: ReadyInfo? = null,
    val runs: List<String> = emptyList(),
    val activeRunId: String? = null,
    val running: Boolean = false,
    val events: List<RunEvent> = emptyList(),
    val lastSequence: Long = 0,
    val gatewayNodes: List<GatewayNode> = emptyList(),
    val selectedNodeId: String? = null,
    val permission: PermissionPrompt? = null,
    val tab: MobileTab = MobileTab.RUNS,
    val error: String? = null,
) {
    val nodes: List<NodeState> get() {
        val result = linkedMapOf<String, NodeState>()
        events.forEach { event ->
            val id = event.nodeId ?: return@forEach
            val previous = result[id] ?: NodeState(id)
            result[id] = previous.copy(
                role = event.role?.takeIf { it.isNotBlank() } ?: previous.role,
                model = event.model?.takeIf { it.isNotBlank() } ?: previous.model,
                routeNodeId = event.routeNodeId?.takeIf { it.isNotBlank() } ?: previous.routeNodeId,
                status = event.status?.takeIf { it.isNotBlank() } ?: previous.status,
                phase = event.phase?.takeIf { it.isNotBlank() } ?: previous.phase,
                message = event.message?.takeIf { it.isNotBlank() } ?: previous.message,
                durationMs = event.durationMs ?: previous.durationMs,
            )
        }
        return result.values.toList()
    }
}

package dev.olli.mobile

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.olli.mobile.ui.theme.*

class MainActivity : ComponentActivity() {
    private val viewModel: MobileViewModel by viewModels()
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent { OlliMobileTheme { MobileApp(viewModel) } }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MobileApp(viewModel: MobileViewModel) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    if (!state.connected) {
        ConnectionScreen(state, viewModel)
        return
    }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Column { Text("O.L.L.I. Mobile"); Text(state.activeRunId ?: state.info?.model.orEmpty(), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant) } },
                actions = { IconButton(onClick = viewModel::disconnect) { Icon(Icons.Default.LinkOff, "연결 해제") } },
            )
        },
        bottomBar = {
            Surface(tonalElevation = 3.dp) {
                Row(Modifier.fillMaxWidth().navigationBarsPadding(), horizontalArrangement = Arrangement.SpaceEvenly) {
                    NavItem(MobileTab.RUNS, state.tab, Icons.Default.History, "실행") { viewModel.selectTab(it) }
                    NavItem(MobileTab.GRAPH, state.tab, Icons.Default.AccountTree, "그래프") { viewModel.selectTab(it) }
                    NavItem(MobileTab.EVENTS, state.tab, Icons.Default.FormatListBulleted, "이벤트") { viewModel.selectTab(it) }
                    NavItem(MobileTab.GATEWAY, state.tab, Icons.Default.Dns, "Gateway") { viewModel.selectTab(it) }
                }
            }
        },
    ) { padding ->
        Box(Modifier.padding(padding).fillMaxSize()) {
            when (state.tab) {
                MobileTab.RUNS -> RunsScreen(state, viewModel)
                MobileTab.GRAPH -> GraphScreen(state, viewModel)
                MobileTab.EVENTS -> EventsScreen(state, viewModel)
                MobileTab.GATEWAY -> GatewayScreen(state, viewModel)
            }
        }
    }
    state.error?.let { error -> AlertDialog(onDismissRequest = viewModel::clearError, confirmButton = { TextButton(onClick = viewModel::clearError) { Text("확인") } }, title = { Text("O.L.L.I. Mobile") }, text = { Text(error) }) }
    state.permission?.let { permission -> PermissionDialog(permission, viewModel) }
}

@Composable private fun NavItem(tab: MobileTab, selected: MobileTab, icon: ImageVector, label: String, onClick: (MobileTab) -> Unit) {
    val isSelected = tab == selected
    TextButton(onClick = { onClick(tab) }, colors = ButtonDefaults.textButtonColors(contentColor = if (isSelected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) { Icon(icon, label); Text(label, style = MaterialTheme.typography.labelSmall) }
    }
}

@Composable private fun ConnectionScreen(state: MobileUiState, viewModel: MobileViewModel) {
    var showToken by remember { mutableStateOf(false) }
    Surface(Modifier.fillMaxSize()) {
        Column(Modifier.safeDrawingPadding().padding(28.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(18.dp)) {
            Spacer(Modifier.height(24.dp))
            Icon(Icons.Default.Hub, null, tint = MaterialTheme.colorScheme.primary, modifier = Modifier.size(52.dp))
            Text("O.L.L.I. Controller 연결", style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.Bold)
            Text("Android 앱은 모델이나 파일 도구를 기기에서 실행하지 않습니다. 사설망의 인증된 Controller API에 연결해 실행을 관찰하고 승인합니다.", color = MaterialTheme.colorScheme.onSurfaceVariant)
            OutlinedTextField(value = state.settings.baseUrl, onValueChange = { viewModel.updateSettings(it, state.settings.token) }, label = { Text("Controller URL") }, supportingText = { Text("배포 빌드는 HTTPS만 허용합니다") }, keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(autoCorrectEnabled = false), singleLine = true, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(value = state.settings.token, onValueChange = { viewModel.updateSettings(state.settings.baseUrl, it) }, label = { Text("Bearer token") }, visualTransformation = if (showToken) androidx.compose.ui.text.input.VisualTransformation.None else androidx.compose.ui.text.input.PasswordVisualTransformation(), trailingIcon = { IconButton(onClick = { showToken = !showToken }) { Icon(if (showToken) Icons.Default.VisibilityOff else Icons.Default.Visibility, "token 표시 전환") } }, singleLine = true, modifier = Modifier.fillMaxWidth())
            Button(onClick = viewModel::connect, enabled = state.settings.baseUrl.isNotBlank() && state.settings.token.length >= 24, modifier = Modifier.fillMaxWidth().height(52.dp)) { Icon(Icons.Default.Link, null); Spacer(Modifier.width(8.dp)); Text("연결") }
            Text("Controller URL만 private preferences에 저장됩니다. 토큰은 앱 프로세스 메모리에만 유지되며 앱을 다시 열면 재입력해야 합니다.", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable private fun RunsScreen(state: MobileUiState, viewModel: MobileViewModel) {
    var prompt by remember { mutableStateOf("") }
    LazyColumn(contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        item {
            ElevatedCard {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text(if (state.running) "실행 중" else "새 실행", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                    OutlinedTextField(value = prompt, onValueChange = { prompt = it }, label = { Text("작업 목표") }, minLines = 3, maxLines = 6, modifier = Modifier.fillMaxWidth())
                    if (state.running) OutlinedButton(onClick = viewModel::cancelRun, colors = ButtonDefaults.outlinedButtonColors(contentColor = MaterialTheme.colorScheme.error), modifier = Modifier.fillMaxWidth()) { Icon(Icons.Default.Stop, null); Text("전체 실행 취소") }
                    else Button(onClick = { viewModel.startRun(prompt) }, enabled = prompt.isNotBlank(), modifier = Modifier.fillMaxWidth()) { Icon(Icons.Default.PlayArrow, null); Text("실행") }
                }
            }
        }
        item { Text("실행 이력", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold) }
        items(state.runs, key = { it }) { runId -> ListItem(headlineContent = { Text(runId, fontFamily = FontFamily.Monospace) }, leadingContent = { Icon(Icons.Default.History, null) }, modifier = Modifier.clickable { viewModel.loadRun(runId) }) }
    }
}

@Composable private fun GraphScreen(state: MobileUiState, viewModel: MobileViewModel) {
    val phases = listOf("planning", "coding", "preflight", "testing", "reviewing", "fixing", "verifying", "done")
    Column(Modifier.fillMaxSize()) {
        Text("Development Graph", style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(16.dp), fontWeight = FontWeight.Bold)
        Row(Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
            phases.forEachIndexed { index, phase ->
                val node = state.nodes.lastOrNull { it.id == phase || it.phase == phase || it.id.contains(phase) }
                NodeCard(phase.replaceFirstChar(Char::uppercase), node, state.selectedNodeId == node?.id) { viewModel.selectNode(node?.id) }
                if (index != phases.lastIndex) { Icon(Icons.Default.ArrowForward, null, tint = MaterialTheme.colorScheme.outline, modifier = Modifier.padding(horizontal = 6.dp)) }
            }
        }
        val reviewers = state.nodes.filter { it.role.contains("reviewer", ignoreCase = true) }
        if (reviewers.isNotEmpty()) {
            Text("Reviewer fan-out", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(16.dp, 20.dp, 16.dp, 8.dp))
            Row(Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) { reviewers.forEach { NodeCard(it.role, it, state.selectedNodeId == it.id) { viewModel.selectNode(it.id) } } }
        }
        state.nodes.firstOrNull { it.id == state.selectedNodeId }?.let { NodeInspector(it) }
    }
}

@Composable private fun NodeCard(title: String, node: NodeState?, selected: Boolean, onClick: () -> Unit) {
    val status = node?.status ?: "pending"
    ElevatedCard(modifier = Modifier.width(150.dp).height(96.dp).semantics { contentDescription = "$title, $status" }.clickable(onClick = onClick), colors = CardDefaults.elevatedCardColors(containerColor = if (selected) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainer)) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) { Icon(statusIcon(status), null, tint = statusColor(status)); Spacer(Modifier.width(6.dp)); Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.SemiBold) }
            Text(node?.model.orEmpty().ifBlank { "대기" }, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
            Text(node?.routeNodeId.orEmpty().ifBlank { "direct" }, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
        }
    }
}

@Composable private fun NodeInspector(node: NodeState) { ElevatedCard(Modifier.padding(16.dp).fillMaxWidth()) { Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) { Text(node.id, style = MaterialTheme.typography.titleMedium, fontFamily = FontFamily.Monospace); Detail("Role", node.role); Detail("Model", node.model); Detail("Gateway", node.routeNodeId.ifBlank { "direct" }); Detail("Status", node.status); if (node.message.isNotBlank()) Text(node.message, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) } } }

@Composable private fun EventsScreen(state: MobileUiState, viewModel: MobileViewModel) { LazyColumn(contentPadding = PaddingValues(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) { item { Text("Live Events · ${state.events.size}", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold, modifier = Modifier.padding(4.dp, 4.dp, 4.dp, 10.dp)) }; items(state.events.asReversed(), key = { it.sequence }) { event -> ListItem(headlineContent = { Text(event.kind.replace('_', ' ')) }, supportingContent = { Column { Text(listOfNotNull(event.nodeId, event.role, event.model).joinToString(" · "), fontFamily = FontFamily.Monospace); event.message?.takeIf(String::isNotBlank)?.let { Text(it, maxLines = 2, overflow = TextOverflow.Ellipsis) } } }, leadingContent = { Icon(statusIcon(event.status ?: event.kind), null, tint = statusColor(event.status ?: event.kind)) }, trailingContent = { Text("#${event.sequence}", style = MaterialTheme.typography.labelSmall) }, modifier = Modifier.clickable { viewModel.selectNode(event.nodeId); viewModel.selectTab(MobileTab.GRAPH) }) } } }

@Composable private fun GatewayScreen(state: MobileUiState, viewModel: MobileViewModel) { LazyColumn(contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) { item { Text("AI Gateway", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) }; if (state.gatewayNodes.isEmpty()) item { Text("Gateway가 비활성화됐거나 노드가 없습니다.", color = MaterialTheme.colorScheme.onSurfaceVariant) }; items(state.gatewayNodes, key = { it.id }) { node -> ElevatedCard { Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) { Row { Icon(statusIcon(gatewayStatus(node)), null, tint = statusColor(gatewayStatus(node))); Spacer(Modifier.width(8.dp)); Text(node.id, fontWeight = FontWeight.Bold); Spacer(Modifier.weight(1f)); Text("${node.active}/${node.limit}") }; LinearProgressIndicator(progress = { node.active.toFloat() / maxOf(1, node.limit) }, modifier = Modifier.fillMaxWidth()); Text(node.models.joinToString(), fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.labelMedium); Text(node.roles.joinToString(), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant); OutlinedButton(onClick = { viewModel.setDrain(node, !node.draining) }, modifier = Modifier.align(Alignment.End)) { Text(if (node.draining) "Resume" else "Drain") } } } } } }

@Composable private fun PermissionDialog(permission: PermissionPrompt, viewModel: MobileViewModel) { AlertDialog(onDismissRequest = {}, icon = { Icon(Icons.Default.AdminPanelSettings, null) }, title = { Text("도구 실행 승인") }, text = { Column(verticalArrangement = Arrangement.spacedBy(10.dp)) { Text(permission.toolName, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.titleMedium); permission.args.entries.sortedBy { it.key }.forEach { Text("${it.key}: ${it.value}", style = MaterialTheme.typography.bodySmall) }; Text("Android 앱도 CLI와 동일한 Permission Engine을 사용합니다.", color = MaterialTheme.colorScheme.onSurfaceVariant) } }, dismissButton = { TextButton(onClick = { viewModel.decidePermission(false) }) { Text("거부") } }, confirmButton = { Row { TextButton(onClick = { viewModel.decidePermission(true, true) }) { Text("항상 허용") }; Button(onClick = { viewModel.decidePermission(true) }) { Text("이번만") } } }) }

@Composable private fun Detail(key: String, value: String) { Row { Text(key, color = MaterialTheme.colorScheme.onSurfaceVariant); Spacer(Modifier.weight(1f)); Text(value.ifBlank { "—" }, fontFamily = FontFamily.Monospace) } }
private fun gatewayStatus(node: GatewayNode) = when { node.draining -> "draining"; node.circuitOpen -> "circuit-open"; node.healthy -> "healthy"; else -> "unhealthy" }
private fun statusColor(status: String): Color = when (status.lowercase()) { "success", "succeeded", "completed", "healthy", "released" -> OlliGreen; "running", "streaming", "leased" -> OlliTeal; "waiting", "pending", "draining" -> OlliAmber; "failed", "unhealthy", "circuit-open", "cancelled" -> OlliRed; else -> Color.Gray }
private fun statusIcon(status: String): ImageVector = when (status.lowercase()) { "success", "succeeded", "completed", "healthy", "released" -> Icons.Default.CheckCircle; "running", "streaming", "leased" -> Icons.Default.RadioButtonChecked; "waiting", "pending", "draining" -> Icons.Default.Schedule; "failed", "unhealthy", "circuit-open" -> Icons.Default.Warning; "cancelled" -> Icons.Default.StopCircle; else -> Icons.Default.Circle }

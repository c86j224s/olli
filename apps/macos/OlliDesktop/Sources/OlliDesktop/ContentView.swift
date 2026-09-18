import SwiftUI

struct ContentView: View {
    @EnvironmentObject private var core: CoreBridge
    @State private var prompt = ""
    @State private var selection: String?
    @State private var inspectorTab = InspectorTab.node

    var body: some View {
        NavigationSplitView {
            Sidebar(selection: $selection)
        } content: {
            VStack(spacing: 0) {
                RunToolbar(prompt: $prompt)
                Divider()
                GraphCanvas(nodes: core.nodeStates, selection: $selection)
                Divider()
                EventTimeline(events: core.events, selection: $selection)
                    .frame(minHeight: 210, idealHeight: 250, maxHeight: 320)
            }
        } detail: {
            Inspector(selection: selection, tab: $inspectorTab)
        }
        .navigationSplitViewStyle(.balanced)
        .frame(minWidth: 1180, minHeight: 760)
        .alert("O.L.L.I. Desktop", isPresented: Binding(get: { core.errorMessage != nil }, set: { if !$0 { core.errorMessage = nil } })) {
            Button("확인", role: .cancel) { core.errorMessage = nil }
        } message: { Text(core.errorMessage ?? "") }
        .sheet(item: $core.permissionPrompt) { permission in
            PermissionSheet(permission: permission)
        }
    }
}

enum InspectorTab: String, CaseIterable, Identifiable {
    case node = "노드"
    case gateway = "Gateway"
    case run = "실행"
    var id: String { rawValue }
}

private struct Sidebar: View {
    @EnvironmentObject private var core: CoreBridge
    @Binding var selection: String?

    var body: some View {
        List(selection: $selection) {
            Section("현재 실행") {
                Label(core.isRunning ? "실행 중" : "대기", systemImage: core.isRunning ? "bolt.horizontal.circle.fill" : "pause.circle")
                    .foregroundStyle(core.isRunning ? OlliTheme.accent : .secondary)
                if let runID = core.activeRunID {
                    Text(runID).font(.caption.monospaced()).textSelection(.enabled)
                }
            }
            Section("실행 이력") {
                ForEach(core.runHistory.prefix(20), id: \.self) { runID in
                    Label(runID, systemImage: "clock.arrow.circlepath")
                        .font(.caption.monospaced())
                        .tag("history:\(runID)")
                        .onTapGesture { core.loadRun(runID) }
                }
            }
            Section("그래프 노드") {
                ForEach(core.nodeStates) { node in
                    Label {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(displayName(node.id)).lineLimit(1)
                            Text(node.role.isEmpty ? node.status : node.role)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    } icon: {
                        Image(systemName: OlliTheme.statusSymbol(node.status))
                            .foregroundStyle(OlliTheme.statusColor(node.status))
                    }
                    .tag(node.id)
                }
            }
            Section("Gateway") {
                ForEach(core.gatewayNodes) { node in
                    Label {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(node.id)
                            Text("\(node.active)/\(node.limit) · \(node.models.joined(separator: ", "))")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    } icon: {
                        Image(systemName: node.healthy ? "server.rack" : "exclamationmark.triangle.fill")
                            .foregroundStyle(node.healthy ? OlliTheme.good : OlliTheme.serious)
                    }
                }
            }
        }
        .navigationTitle("O.L.L.I.")
        .toolbar {
            ToolbarItem { Button { core.refreshGateway() } label: { Image(systemName: "arrow.clockwise") }.help("Gateway 상태 새로 고침") }
        }
    }

    private func displayName(_ id: String) -> String {
        id.replacingOccurrences(of: "team-", with: "").replacingOccurrences(of: "subagent_", with: "")
    }
}

private struct RunToolbar: View {
    @EnvironmentObject private var core: CoreBridge
    @Binding var prompt: String

    var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(core.isRunning ? "실행 중" : "새 실행")
                    .font(.headline)
                Text(core.isReady ? "\(core.model) · \(core.workspace)" : "Desktop Core 연결 중…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 18)
            TextField("작업 목표를 입력하세요", text: $prompt)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 380, maxWidth: 620)
                .onSubmit { core.startRun(prompt: prompt) }
            if core.isRunning {
                Button(role: .destructive) { core.cancelRun() } label: { Label("취소", systemImage: "stop.fill") }
            } else {
                Button { core.startRun(prompt: prompt) } label: { Label("실행", systemImage: "play.fill") }
                    .buttonStyle(.borderedProminent)
                    .tint(OlliTheme.accent)
                    .disabled(!core.isReady || prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(16)
    }
}

private struct GraphCanvas: View {
    let nodes: [NodeViewState]
    @Binding var selection: String?

    private let phases = ["planning", "coding", "preflight", "testing", "reviewing", "fixing", "verifying", "done"]

    var body: some View {
        GeometryReader { geometry in
            ScrollView([.horizontal, .vertical]) {
                ZStack(alignment: .topLeading) {
                    let positioned = positions(in: geometry.size)
                    Canvas { context, _ in
                        for index in 0..<(phases.count - 1) {
                            guard let from = positioned[phases[index]], let to = positioned[phases[index + 1]] else { continue }
                            var path = Path()
                            path.move(to: CGPoint(x: from.x + 80, y: from.y + 42))
                            path.addLine(to: CGPoint(x: to.x - 80, y: to.y + 42))
                            context.stroke(path, with: .color(.secondary.opacity(0.25)), style: StrokeStyle(lineWidth: 2, dash: [5, 6]))
                        }
                    }
                    .accessibilityHidden(true)
                    ForEach(phases, id: \.self) { phase in
                        let state = bestState(for: phase)
                        NodeCard(title: phase.capitalized, state: state, selected: selection == state?.id)
                            .frame(width: 160, height: 84)
                            .position(positioned[phase] ?? .zero)
                            .onTapGesture { if let state { selection = state.id } }
                    }
                    reviewerFanout(positioned: positioned)
                }
                .frame(width: max(1040, geometry.size.width), height: max(390, geometry.size.height))
                .padding(24)
            }
        }
        .background(.background)
        .overlay(alignment: .topLeading) {
            VStack(alignment: .leading, spacing: 3) {
                Text("Development Graph").font(.headline)
                Text("노드 상태는 색과 아이콘으로 함께 표시됩니다.").font(.caption).foregroundStyle(.secondary)
            }.padding(16)
        }
    }

    @ViewBuilder private func reviewerFanout(positioned: [String: CGPoint]) -> some View {
        if let center = positioned["reviewing"] {
            let reviewers = nodes.filter { $0.role.lowercased().contains("reviewer") && $0.id != "reviewing" }
            ForEach(Array(reviewers.enumerated()), id: \.element.id) { index, reviewer in
                NodeCard(title: reviewer.role.replacingOccurrences(of: "Reviewer", with: ""), state: reviewer, selected: selection == reviewer.id)
                    .frame(width: 142, height: 70)
                    .position(x: center.x + CGFloat((index % 2) * 158 - 79), y: center.y + CGFloat(125 + (index / 2) * 82))
                    .onTapGesture { selection = reviewer.id }
            }
        }
    }

    private func positions(in size: CGSize) -> [String: CGPoint] {
        let width = max(size.width, 1040)
        let y = max(130, size.height * 0.42)
        let gap = (width - 180) / CGFloat(phases.count - 1)
        return Dictionary(uniqueKeysWithValues: phases.enumerated().map { index, phase in
            (phase, CGPoint(x: 90 + CGFloat(index) * gap, y: y))
        })
    }

    private func bestState(for phase: String) -> NodeViewState? {
        nodes.last { $0.id == phase || $0.phase == phase || $0.id.contains(phase) }
    }
}

private struct NodeCard: View {
    let title: String
    let state: NodeViewState?
    let selected: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: OlliTheme.statusSymbol(state?.status ?? "pending"))
                    .foregroundStyle(OlliTheme.statusColor(state?.status ?? "pending"))
                Text(title).font(.headline).lineLimit(1)
                Spacer()
            }
            if let state {
                Text([state.model, state.routeNodeID].filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            } else {
                Text("대기").font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(12)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(selected ? OlliTheme.accent : .secondary.opacity(0.18), lineWidth: selected ? 2 : 1))
        .shadow(color: .black.opacity(0.06), radius: 6, y: 2)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(title), \(state?.status ?? "pending")")
    }
}

private struct EventTimeline: View {
    let events: [RunEvent]
    @Binding var selection: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Live Events").font(.headline)
                Spacer()
                Text("\(events.count)").font(.caption.monospaced()).foregroundStyle(.secondary)
            }.padding(.horizontal, 16).padding(.vertical, 10)
            List(events.suffix(250).reversed()) { event in
                HStack(alignment: .top, spacing: 10) {
                    Image(systemName: OlliTheme.statusSymbol(event.status ?? event.kind))
                        .foregroundStyle(OlliTheme.statusColor(event.status ?? event.kind))
                        .frame(width: 18)
                    VStack(alignment: .leading, spacing: 3) {
                        HStack {
                            Text(event.kind.replacingOccurrences(of: "_", with: " ")).font(.subheadline.weight(.medium))
                            if let node = event.nodeID { Text(node).font(.caption.monospaced()).foregroundStyle(.secondary) }
                            Spacer()
                            Text(event.timestamp.formatted(date: .omitted, time: .standard)).font(.caption).foregroundStyle(.tertiary)
                        }
                        if let message = event.message, !message.isEmpty {
                            Text(message).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                        }
                    }
                }
                .contentShape(Rectangle())
                .onTapGesture { selection = event.nodeID }
            }
            .listStyle(.inset)
        }
    }
}

private struct Inspector: View {
    @EnvironmentObject private var core: CoreBridge
    let selection: String?
    @Binding var tab: InspectorTab

    var body: some View {
        VStack(spacing: 0) {
            Picker("Inspector", selection: $tab) {
                ForEach(InspectorTab.allCases) { item in Text(item.rawValue).tag(item) }
            }
            .pickerStyle(.segmented)
            .padding(14)
            Divider()
            switch tab {
            case .node: nodeInspector
            case .gateway: gatewayInspector
            case .run: runInspector
            }
        }
        .navigationTitle("Inspector")
    }

    private var nodeInspector: some View {
        ScrollView {
            if let node = core.nodeStates.first(where: { $0.id == selection }) {
                VStack(alignment: .leading, spacing: 16) {
                    InspectorHeader(title: node.id, status: node.status)
                    KeyValue("Role", node.role)
                    KeyValue("Model", node.model)
                    KeyValue("Gateway node", node.routeNodeID.isEmpty ? "direct" : node.routeNodeID)
                    KeyValue("Phase", node.phase)
                    KeyValue("Duration", node.durationMS > 0 ? "\(node.durationMS) ms" : "—")
                    if !node.lastMessage.isEmpty { DetailBlock(title: "Latest message", text: node.lastMessage) }
                    let events = core.events.filter { $0.nodeID == node.id }
                    DetailBlock(title: "Events", text: events.suffix(30).map { "#\($0.sequence) · \($0.kind) · \($0.status ?? "")" }.joined(separator: "\n"))
                }.padding(18)
            } else {
                ContentUnavailableView("노드를 선택하세요", systemImage: "point.3.connected.trianglepath.dotted", description: Text("그래프 또는 사이드바에서 노드를 선택하면 모델, 라우팅, 상태와 이벤트를 확인할 수 있습니다."))
            }
        }
    }

    private var gatewayInspector: some View {
        List(core.gatewayNodes) { node in
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Label(node.id, systemImage: OlliTheme.statusSymbol(nodeStatus(node)))
                        .font(.headline)
                        .foregroundStyle(OlliTheme.statusColor(nodeStatus(node)))
                    Spacer()
                    Text("\(node.active)/\(node.limit)").monospacedDigit()
                }
                ProgressView(value: Double(node.active), total: Double(max(1, node.limit))).tint(OlliTheme.accent)
                Text(node.models.joined(separator: ", ")).font(.caption.monospaced()).foregroundStyle(.secondary)
                Text(node.roles.joined(separator: ", ")).font(.caption).foregroundStyle(.secondary)
                HStack {
                    Button(node.draining ? "Resume" : "Drain") { core.setDrain(nodeID: node.id, draining: !node.draining) }
                    Spacer()
                    Text(node.endpoint).font(.caption2).foregroundStyle(.tertiary).lineLimit(1)
                }
            }.padding(.vertical, 6)
        }.overlay { if core.gatewayNodes.isEmpty { ContentUnavailableView("Gateway가 비활성화됨", systemImage: "server.rack", description: Text("단일 Ollama 모드에서는 Gateway 노드가 표시되지 않습니다.")) } }
    }

    private var runInspector: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                InspectorHeader(title: core.activeRunID ?? "No active run", status: core.isRunning ? "running" : "idle")
                KeyValue("Workspace", core.workspace)
                KeyValue("Main model", core.model)
                KeyValue("Events", String(core.events.count))
                let routes = core.events.filter { $0.kind == "model_routed" }.compactMap(\.routeNodeID)
                KeyValue("Routed leases", String(routes.count))
                DetailBlock(title: "Route history", text: routes.isEmpty ? "No routed leases" : routes.joined(separator: "\n"))
            }.padding(18)
        }
    }

    private func nodeStatus(_ node: GatewayNode) -> String {
        if node.draining { return "draining" }
        if node.circuitOpen { return "circuit-open" }
        return node.healthy ? "healthy" : "unhealthy"
    }
}

private struct InspectorHeader: View {
    let title: String
    let status: String
    var body: some View {
        HStack {
            VStack(alignment: .leading) { Text(title).font(.title2.bold()); Text(status.capitalized).foregroundStyle(.secondary) }
            Spacer()
            Image(systemName: OlliTheme.statusSymbol(status)).font(.title).foregroundStyle(OlliTheme.statusColor(status))
        }
    }
}

private struct KeyValue: View {
    let key: String, value: String
    init(_ key: String, _ value: String) { self.key = key; self.value = value }
    var body: some View { HStack(alignment: .firstTextBaseline) { Text(key).foregroundStyle(.secondary); Spacer(); Text(value.isEmpty ? "—" : value).multilineTextAlignment(.trailing).textSelection(.enabled) } }
}

private struct DetailBlock: View {
    let title: String, text: String
    var body: some View { VStack(alignment: .leading, spacing: 8) { Text(title).font(.headline); Text(text).font(.caption.monospaced()).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading).padding(12).background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 10)) } }
}

private struct PermissionSheet: View {
    @EnvironmentObject private var core: CoreBridge
    let permission: PermissionPrompt
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Label("도구 실행 승인", systemImage: "hand.raised.fill").font(.title2.bold())
            Text(permission.toolName).font(.title3.monospaced())
            if !permission.args.isEmpty { DetailBlock(title: "Arguments", text: permission.args.map { "\($0.key): \($0.value)" }.sorted().joined(separator: "\n")) }
            Text("Desktop 앱도 CLI와 동일한 Permission Engine을 사용합니다. 승인하지 않으면 도구는 실행되지 않습니다.").font(.callout).foregroundStyle(.secondary)
            HStack {
                Button("거부", role: .cancel) { core.decidePermission(allowed: false) }
                Spacer()
                Button("항상 허용") { core.decidePermission(allowed: true, always: true) }
                Button("이번만 허용") { core.decidePermission(allowed: true) }.buttonStyle(.borderedProminent).tint(OlliTheme.accent)
            }
        }.padding(24).frame(width: 520)
    }
}

import Foundation

@MainActor
final class CoreBridge: ObservableObject {
    @Published var events: [RunEvent] = []
    @Published var gatewayNodes: [GatewayNode] = []
    @Published var isReady = false
    @Published var isRunning = false
    @Published var activeRunID: String?
    @Published var errorMessage: String?
    @Published var permissionPrompt: PermissionPrompt?
    @Published var runHistory: [String] = []
    @Published var workspace = ""
    @Published var model = ""

    private var process: Process?
    private var stdin: FileHandle?
    private var nextID = 0
    private var pendingMethods: [String: String] = [:]
    private let decoder: JSONDecoder = {
        let value = JSONDecoder()
        value.dateDecodingStrategy = .iso8601
        return value
    }()

    func start() {
        guard process == nil else { return }
        do {
            let executable = try locateCoreExecutable()
            let root = try locateWorkspaceRoot(from: executable)
            workspace = root.path
            let process = Process()
            let input = Pipe()
            let output = Pipe()
            let error = Pipe()
            process.executableURL = executable
            process.arguments = ["--workspace", root.path, "--config", root.appendingPathComponent("config.json").path]
            process.standardInput = input
            process.standardOutput = output
            process.standardError = error
            process.terminationHandler = { [weak self] process in
                Task { @MainActor in
                    self?.isReady = false
                    self?.isRunning = false
                    if process.terminationStatus != 0 {
                        self?.errorMessage = "Desktop core exited with status \(process.terminationStatus)."
                    }
                }
            }
            output.fileHandleForReading.readabilityHandler = { [weak self] handle in
                let data = handle.availableData
                if data.isEmpty { return }
                Task { @MainActor in self?.consume(data) }
            }
            error.fileHandleForReading.readabilityHandler = { [weak self] handle in
                let data = handle.availableData
                guard !data.isEmpty, let text = String(data: data, encoding: .utf8) else { return }
                Task { @MainActor in self?.errorMessage = text.trimmingCharacters(in: .whitespacesAndNewlines) }
            }
            try process.run()
            self.process = process
            stdin = input.fileHandleForWriting
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func stop() {
        process?.terminate()
        process = nil
        stdin = nil
        isReady = false
        isRunning = false
    }

    func startRun(prompt: String) {
        let value = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty, !isRunning else { return }
        send(method: "start_run", prompt: value)
    }

    func cancelRun() { send(method: "cancel_run") }
    func refreshGateway() { send(method: "gateway_status") }
    func refreshRunHistory() { send(method: "list_runs") }
    func loadRun(_ runID: String) { send(method: "snapshot", runID: runID) }
    func setDrain(nodeID: String, draining: Bool) { send(method: draining ? "gateway_drain" : "gateway_resume", nodeID: nodeID) }

    func decidePermission(allowed: Bool, always: Bool = false) {
        guard let prompt = permissionPrompt else { return }
        send(method: "permission_decision", id: prompt.id, allowed: allowed, always: always)
        permissionPrompt = nil
    }

    var nodeStates: [NodeViewState] {
        var states: [String: NodeViewState] = [:]
        for event in events {
            guard let nodeID = event.nodeID, !nodeID.isEmpty else { continue }
            var state = states[nodeID] ?? NodeViewState(id: nodeID, role: event.role ?? "", model: event.model ?? "", routeNodeID: event.routeNodeID ?? "", status: event.status ?? "pending", phase: event.phase ?? "", lastMessage: "", durationMS: 0)
            if let role = event.role, !role.isEmpty { state.role = role }
            if let model = event.model, !model.isEmpty { state.model = model }
            if let route = event.routeNodeID, !route.isEmpty { state.routeNodeID = route }
            if let status = event.status, !status.isEmpty { state.status = status }
            if let phase = event.phase, !phase.isEmpty { state.phase = phase }
            if let message = event.message, !message.isEmpty { state.lastMessage = message }
            if let duration = event.durationMS { state.durationMS = duration }
            states[nodeID] = state
        }
        return states.values.sorted { phaseOrder($0.id) < phaseOrder($1.id) }
    }

    private func phaseOrder(_ value: String) -> Int {
        let phases = ["planning", "coding", "preflight", "testing", "reviewing", "fixing", "verifying", "done", "failed"]
        return phases.firstIndex(of: value) ?? (100 + value.hashValue)
    }

    private func send(method: String, id explicitID: String? = nil, prompt: String? = nil, runID: String? = nil, nodeID: String? = nil, allowed: Bool? = nil, always: Bool? = nil) {
        guard let stdin else { return }
        nextID += 1
        let id = explicitID ?? "request-\(nextID)"
        if explicitID == nil { pendingMethods[id] = method }
        let request = DesktopRequest(id: id, method: method, prompt: prompt, runID: runID, nodeID: nodeID, allowed: allowed, always: always)
        do {
            var data = try JSONEncoder().encode(request)
            data.append(0x0A)
            try stdin.write(contentsOf: data)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private var partial = Data()
    private func consume(_ data: Data) {
        partial.append(data)
        while let newline = partial.firstIndex(of: 0x0A) {
            let line = partial[..<newline]
            partial.removeSubrange(...newline)
            guard !line.isEmpty else { continue }
            do {
                let envelope = try decoder.decode(Envelope.self, from: Data(line))
                handle(envelope)
            } catch {
                errorMessage = "Desktop event decode failed: \(error.localizedDescription)"
            }
        }
    }

    private func handle(_ envelope: Envelope) {
        switch envelope.type {
        case "ready":
            isReady = true
            if case .object(let result)? = envelope.result {
                if case .string(let value)? = result["workspace"] { workspace = value }
                if case .string(let value)? = result["model"] { model = value }
            }
            refreshGateway()
            refreshRunHistory()
        case "event":
            guard let event = envelope.event else { return }
            events.append(event)
            if events.count > 4096 { events.removeFirst(events.count - 4096) }
            if event.kind == "run_started" {
                activeRunID = event.runID
                isRunning = true
            } else if ["run_completed", "run_failed", "run_cancelled"].contains(event.kind) {
                isRunning = false
            }
        case "permission":
            if case .object(let result)? = envelope.result,
               case .string(let id)? = result["id"],
               case .string(let runID)? = result["run_id"],
               case .string(let tool)? = result["tool_name"] {
                var args: [String: JSONValue] = [:]
                if case .object(let value)? = result["args"] { args = value }
                permissionPrompt = PermissionPrompt(id: id, runID: runID, toolName: tool, args: args)
            }
        case "response":
            if let error = envelope.error, !error.isEmpty { errorMessage = error }
            let method = envelope.id.flatMap { pendingMethods.removeValue(forKey: $0) }
            if method == "start_run", case .object(let result)? = envelope.result, case .string(let runID)? = result["run_id"] {
                activeRunID = runID
                isRunning = true
                events.removeAll()
            }
            if method == "gateway_status", case .array(let values)? = envelope.result {
                gatewayNodes = values.compactMap(decodeGatewayNode)
            }
            if method == "list_runs", case .array(let values)? = envelope.result {
                runHistory = values.compactMap { if case .string(let value) = $0 { return value }; return nil }.sorted().reversed()
            }
            if method == "snapshot", case .array(let values)? = envelope.result {
                let replayed = values.compactMap(decodeRunEvent)
                if !replayed.isEmpty {
                    events = replayed
                    activeRunID = replayed.first?.runID
                    isRunning = false
                }
            }
        default: break
        }
    }

    private func decodeRunEvent(_ value: JSONValue) -> RunEvent? {
        guard case .object(let object) = value,
              let data = try? JSONEncoder().encode(object) else { return nil }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try? decoder.decode(RunEvent.self, from: data)
    }

    private func decodeGatewayNode(_ value: JSONValue) -> GatewayNode? {
        guard case .object(let object) = value,
              let data = try? JSONEncoder().encode(object) else { return nil }
        return try? JSONDecoder().decode(GatewayNode.self, from: data)
    }

    private func locateCoreExecutable() throws -> URL {
        if let configured = ProcessInfo.processInfo.environment["OLLI_DESKTOP_CORE"], !configured.isEmpty {
            return URL(fileURLWithPath: configured)
        }
        let bundleCandidate = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/olli-desktop-core")
        if FileManager.default.isExecutableFile(atPath: bundleCandidate.path) { return bundleCandidate }
        let root = try locateWorkspaceRoot(from: URL(fileURLWithPath: FileManager.default.currentDirectoryPath))
        let development = root.appendingPathComponent("bin/olli-desktop-core")
        if FileManager.default.isExecutableFile(atPath: development.path) { return development }
        throw NSError(domain: "OlliDesktop", code: 1, userInfo: [NSLocalizedDescriptionKey: "Build bin/olli-desktop-core first or set OLLI_DESKTOP_CORE."])
    }

    private func locateWorkspaceRoot(from start: URL) throws -> URL {
        var current = start.hasDirectoryPath ? start : start.deletingLastPathComponent()
        for _ in 0..<8 {
            if FileManager.default.fileExists(atPath: current.appendingPathComponent("go.mod").path),
               FileManager.default.fileExists(atPath: current.appendingPathComponent("config.json").path) { return current }
            current.deleteLastPathComponent()
        }
        throw NSError(domain: "OlliDesktop", code: 2, userInfo: [NSLocalizedDescriptionKey: "Could not locate the O.L.L.I. workspace root."])
    }
}

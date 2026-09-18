import Foundation

struct RunEvent: Codable, Identifiable, Sendable {
    let sequence: UInt64
    let timestamp: Date
    let runID: String
    let kind: String
    let graphID: String?
    let nodeID: String?
    let parentID: String?
    let phase: String?
    let role: String?
    let model: String?
    let routeNodeID: String?
    let status: String?
    let message: String?
    let toolName: String?
    let durationMS: Int64?
    let metadata: [String: JSONValue]?

    var id: UInt64 { sequence }

    enum CodingKeys: String, CodingKey {
        case sequence, timestamp, kind, status, message, role, model, phase, metadata
        case runID = "run_id"
        case graphID = "graph_id"
        case nodeID = "node_id"
        case parentID = "parent_id"
        case routeNodeID = "route_node_id"
        case toolName = "tool_name"
        case durationMS = "duration_ms"
    }
}

enum JSONValue: Codable, Sendable, CustomStringConvertible {
    case string(String), number(Double), bool(Bool), array([JSONValue]), object([String: JSONValue]), null

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() { self = .null }
        else if let value = try? container.decode(Bool.self) { self = .bool(value) }
        else if let value = try? container.decode(Double.self) { self = .number(value) }
        else if let value = try? container.decode(String.self) { self = .string(value) }
        else if let value = try? container.decode([JSONValue].self) { self = .array(value) }
        else { self = .object(try container.decode([String: JSONValue].self)) }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value): try container.encode(value)
        case .number(let value): try container.encode(value)
        case .bool(let value): try container.encode(value)
        case .array(let value): try container.encode(value)
        case .object(let value): try container.encode(value)
        case .null: try container.encodeNil()
        }
    }

    var description: String {
        switch self {
        case .string(let value): value
        case .number(let value): String(value)
        case .bool(let value): String(value)
        case .array(let value): value.map(\.description).joined(separator: ", ")
        case .object(let value): value.map { "\($0.key): \($0.value)" }.sorted().joined(separator: ", ")
        case .null: "null"
        }
    }
}

struct GatewayNode: Codable, Identifiable, Sendable {
    let id: String
    let endpoint: String
    let healthy: Bool
    let draining: Bool
    let active: Int
    let limit: Int
    let models: [String]
    let roles: [String]
    let latency: Int64
    let failures: Int
    let circuitOpen: Bool
    let lastError: String?

    enum CodingKeys: String, CodingKey {
        case id, endpoint, healthy, draining, active, limit, models, roles, latency, failures
        case circuitOpen = "circuit_open"
        case lastError = "last_error"
    }
}

struct PermissionPrompt: Identifiable, Sendable {
    let id: String
    let runID: String
    let toolName: String
    let args: [String: JSONValue]
}

struct DesktopRequest: Encodable {
    let id: String
    let method: String
    let prompt: String?
    let runID: String?
    let nodeID: String?
    let allowed: Bool?
    let always: Bool?

    enum CodingKeys: String, CodingKey {
        case id, method, prompt, allowed, always
        case runID = "run_id"
        case nodeID = "node_id"
    }
}

struct Envelope: Decodable {
    let type: String
    let id: String?
    let ok: Bool?
    let error: String?
    let result: JSONValue?
    let event: RunEvent?
}

struct NodeViewState: Identifiable, Hashable {
    let id: String
    var role: String
    var model: String
    var routeNodeID: String
    var status: String
    var phase: String
    var lastMessage: String
    var durationMS: Int64
}

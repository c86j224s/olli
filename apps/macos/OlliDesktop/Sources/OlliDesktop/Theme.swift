import SwiftUI

enum OlliTheme {
    static let accent = Color(red: 0.13, green: 0.61, blue: 0.55)
    static let accentSecondary = Color(red: 0.27, green: 0.46, blue: 0.78)
    static let good = Color(red: 0.20, green: 0.62, blue: 0.36)
    static let warning = Color(red: 0.88, green: 0.58, blue: 0.12)
    static let serious = Color(red: 0.84, green: 0.30, blue: 0.22)
    static let idle = Color.secondary.opacity(0.55)

    static func statusColor(_ status: String) -> Color {
        switch status.lowercased() {
        case "success", "succeeded", "completed", "healthy", "released": good
        case "running", "streaming", "leased": accent
        case "waiting", "pending", "draining": warning
        case "failed", "unhealthy", "circuit-open", "cancelled": serious
        default: idle
        }
    }

    static func statusSymbol(_ status: String) -> String {
        switch status.lowercased() {
        case "success", "succeeded", "completed", "healthy", "released": "checkmark.circle.fill"
        case "running", "streaming", "leased": "waveform.circle.fill"
        case "waiting", "pending", "draining": "clock.fill"
        case "failed", "unhealthy", "circuit-open": "exclamationmark.triangle.fill"
        case "cancelled": "stop.circle.fill"
        default: "circle.fill"
        }
    }
}

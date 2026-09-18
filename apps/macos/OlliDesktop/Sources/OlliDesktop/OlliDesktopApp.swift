import SwiftUI

@main
struct OlliDesktopApp: App {
    @StateObject private var core = CoreBridge()

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(core)
                .task { core.start() }
                .onDisappear { core.stop() }
        }
        .defaultSize(width: 1440, height: 900)
        .commands {
            CommandGroup(after: .appInfo) {
                Button("Gateway 상태 새로 고침") { core.refreshGateway() }
                    .keyboardShortcut("r", modifiers: [.command, .shift])
                Button("현재 실행 취소") { core.cancelRun() }
                    .keyboardShortcut(".", modifiers: [.command])
                    .disabled(!core.isRunning)
            }
        }
    }
}

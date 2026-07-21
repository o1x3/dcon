import SwiftUI

/// Menubar dropdown: backend status, running containers with quick actions,
/// and shortcuts into the main window.
struct MenuBarView: View {
    @EnvironmentObject var state: AppState
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Group {
            if state.systemStatus.isRunning {
                Button("Stop Backend") { state.performDetached(["system", "stop"]) }
            } else {
                Button("Start Backend") { state.performDetached(["system", "start"]) }
            }
            Text("Backend: \(state.systemStatus.label)")

            Divider()

            if state.runningContainers.isEmpty {
                Text("No running containers")
            } else {
                ForEach(state.runningContainers) { c in
                    Menu("\(c.Names.isEmpty ? c.shortID : c.Names)") {
                        Button("Stop") { state.performDetached(["stop", c.ID]) }
                        Button("Restart") { state.performDetached(["restart", c.ID]) }
                        Button("Open Shell") { TerminalLauncher.run(dconArgs: ["exec", "-it", c.ID, "/bin/sh"]) }
                    }
                }
            }

            Divider()

            Button("Open Dcon") {
                NSApp.activate(ignoringOtherApps: true)
                openWindow(id: "main")
            }
            Button("Quit") { NSApp.terminate(nil) }
        }
    }
}

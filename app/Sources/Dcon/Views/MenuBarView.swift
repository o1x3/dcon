import SwiftUI

/// Menubar dropdown: backend status, running containers with quick actions,
/// quick warm-up, and shortcuts into the main window.
struct MenuBarView: View {
    @EnvironmentObject var state: AppState
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Group {
            Text(headline)

            if state.runtimeAvailable {
                if state.systemStatus.isRunning {
                    Button("Stop Backend") { state.performDetached(["system", "stop"]) }
                } else {
                    Button("Start Backend") { state.performDetached(["system", "start"]) }
                }
            } else {
                Link("Install Apple container runtime…",
                     destination: URL(string: "https://github.com/apple/container/releases")!)
            }

            Divider()

            if state.systemStatus.isRunning {
                if state.runningContainers.isEmpty {
                    Text("No running containers")
                } else {
                    ForEach(state.runningContainers.prefix(10)) { c in
                        Menu(menuTitle(for: c)) {
                            Text(c.Image)
                            Divider()
                            Button("Open Shell") { TerminalLauncher.run(dconArgs: ["exec", "-it", c.ID, "/bin/sh"]) }
                            Button("Stop") { state.performDetached(["stop", c.ID]) }
                            Button("Restart") { state.performDetached(["restart", c.ID]) }
                            Button("View in Dcon") { open(section: .containers) }
                        }
                    }
                    if state.runningContainers.count > 10 {
                        Button("\(state.runningContainers.count - 10) more…") { open(section: .containers) }
                    }
                }
                Divider()
            }

            Button("Containers") { open(section: .containers) }
                .keyboardShortcut("1")
            Button("Images") { open(section: .images) }
                .keyboardShortcut("2")
            Button("Machines") { open(section: .machines) }

            Divider()

            Button("Open Dcon") { open(section: state.section) }
                .keyboardShortcut("o")
            SettingsLink { Text("Settings…") }
            Button("Quit Dcon") { NSApp.terminate(nil) }
                .keyboardShortcut("q")
        }
    }

    private var headline: String {
        guard state.runtimeAvailable else { return "Runtime not installed" }
        guard state.systemStatus.isRunning else { return "Backend stopped" }
        let n = state.runningContainers.count
        return n == 0 ? "Backend running" : "Backend running — \(n) container\(n == 1 ? "" : "s")"
    }

    private func menuTitle(for c: ContainerRow) -> String {
        c.Names.isEmpty ? c.shortID : c.Names
    }

    private func open(section: SidebarSection) {
        state.section = section
        NSApp.activate(ignoringOtherApps: true)
        openWindow(id: "main")
    }
}

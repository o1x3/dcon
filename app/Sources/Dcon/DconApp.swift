import SwiftUI

@main
struct DconApp: App {
    @StateObject private var state = AppState()
    @AppStorage(AppSettings.appearanceKey) private var appearance = AppearanceChoice.system.rawValue
    @AppStorage(AppSettings.startBackendOnLaunchKey) private var startBackendOnLaunch = false

    var body: some Scene {
        WindowGroup("Dcon", id: "main") {
            MainWindow()
                .environmentObject(state)
                .frame(minWidth: 940, minHeight: 580)
                .task {
                    (AppearanceChoice(rawValue: appearance) ?? .system).apply()
                    if startBackendOnLaunch {
                        state.performDetached(["system", "start"])
                    }
                    state.startPolling()
                }
        }
        .windowToolbarStyle(.unified)
        .commands { AppCommands(state: state) }

        Settings {
            SettingsView()
                .environmentObject(state)
        }

        MenuBarExtra {
            MenuBarView()
                .environmentObject(state)
        } label: {
            if state.systemStatus.isRunning && !state.runningContainers.isEmpty {
                Label("\(state.runningContainers.count)", systemImage: "shippingbox.fill")
                    .labelStyle(.titleAndIcon)
            } else {
                Image(systemName: state.systemStatus.isRunning ? "shippingbox.fill" : "shippingbox")
            }
        }
    }
}

/// Main-menu commands and keyboard shortcuts.
struct AppCommands: Commands {
    @ObservedObject var state: AppState
    @Environment(\.openWindow) private var openWindow

    var body: some Commands {
        CommandGroup(after: .newItem) {
            Button("Refresh") { Task { await state.refreshAll() } }
                .keyboardShortcut("r", modifiers: .command)
            Divider()
            if state.systemStatus.isRunning {
                Button("Stop Backend") { state.performDetached(["system", "stop"]) }
            } else {
                Button("Start Backend") { state.performDetached(["system", "start"]) }
            }
        }

        CommandMenu("Go") {
            ForEach(Array(SidebarSection.allCases.enumerated()), id: \.element) { idx, section in
                Button(section.rawValue) {
                    state.section = section
                    NSApp.activate(ignoringOtherApps: true)
                    openWindow(id: "main")
                }
                .keyboardShortcut(KeyEquivalent(Character("\(idx + 1)")), modifiers: .command)
            }
        }
    }
}

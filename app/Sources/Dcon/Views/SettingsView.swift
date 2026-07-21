import SwiftUI

/// App preferences (⌘,): polling cadence, CLI location, appearance.
struct SettingsView: View {
    @EnvironmentObject var state: AppState
    @AppStorage(AppSettings.pollIntervalKey) private var pollInterval = 3.0
    @AppStorage(AppSettings.dconPathKey) private var dconPath = ""
    @AppStorage(AppSettings.appearanceKey) private var appearance = AppearanceChoice.system.rawValue
    @AppStorage(AppSettings.startBackendOnLaunchKey) private var startBackendOnLaunch = false

    var body: some View {
        Form {
            Section("General") {
                Picker("Appearance", selection: $appearance) {
                    ForEach(AppearanceChoice.allCases) { c in
                        Text(c.label).tag(c.rawValue)
                    }
                }
                .onChange(of: appearance) { _, new in
                    (AppearanceChoice(rawValue: new) ?? .system).apply()
                }
                Toggle("Start backend when the app launches", isOn: $startBackendOnLaunch)
            }

            Section("Refresh") {
                VStack(alignment: .leading) {
                    Slider(value: $pollInterval, in: 1...15, step: 1) {
                        Text("Refresh every \(Int(pollInterval))s")
                    }
                    Text("How often the app re-reads containers, images, and backend state.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            Section("CLI") {
                HStack {
                    TextField("dcon binary path", text: $dconPath, prompt: Text("automatic"))
                        .textFieldStyle(.roundedBorder)
                    Button("Choose…") {
                        let panel = NSOpenPanel()
                        panel.canChooseDirectories = false
                        panel.allowsMultipleSelection = false
                        panel.directoryURL = URL(fileURLWithPath: "/usr/local/bin")
                        if panel.runModal() == .OK, let url = panel.url {
                            dconPath = url.path
                        }
                    }
                }
                .onChange(of: dconPath) { _, _ in
                    state.cli.rediscover()
                    Task { await state.refreshAll() }
                }
                LabeledContent("Using") {
                    Text(state.cli.binaryURL?.path ?? "not found")
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .foregroundStyle(state.cli.binaryURL == nil ? .red : .secondary)
                }
            }
        }
        .formStyle(.grouped)
        .frame(width: 480)
        .fixedSize(horizontal: false, vertical: true)
    }
}

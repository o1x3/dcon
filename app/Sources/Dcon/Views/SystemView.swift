import SwiftUI

/// Backend/runtime control center: start/stop the Apple `container` backend,
/// inspect disk usage, prune unused data, run diagnostics, tail live events,
/// and manage registry logins.
struct SystemView: View {
    @EnvironmentObject var state: AppState

    @State private var dfRows: [DFRow] = []
    @State private var dfLoading = false

    @State private var pruneAllImages = false
    @State private var pruneVolumes = false
    @State private var showPruneConfirm = false

    @State private var outputSheet: OutputSheetRequest?

    @State private var eventLines: [String] = []
    @State private var eventStream: StreamHandle?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                statusCard
                diskUsageCard
                pruneCard
                diagnosticsCard
                eventsCard
                registryCard
            }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .navigationTitle("System")
        .task { await loadDiskUsage() }
        .onDisappear {
            eventStream?.terminate()
            eventStream = nil
        }
        .sheet(item: $outputSheet) { req in
            CommandOutputSheet(title: req.title, args: req.args)
        }
    }

    // MARK: - Status

    private var statusCard: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 8) {
                    Circle()
                        .fill(statusColor)
                        .frame(width: 10, height: 10)
                    Text(state.systemStatus.label)
                        .font(.title2.bold())
                    Spacer()
                }
                HStack(spacing: 10) {
                    Button {
                        state.performDetached(["system", "start"])
                    } label: {
                        Label("Start", systemImage: "play.fill")
                    }
                    .disabled(!state.runtimeAvailable || state.systemStatus.isRunning)

                    Button {
                        state.performDetached(["system", "stop"])
                    } label: {
                        Label("Stop", systemImage: "stop.fill")
                    }
                    .disabled(!state.systemStatus.isRunning)

                    Button {
                        Task { await state.perform(["system", "kernel", "set", "--recommended"]) }
                    } label: {
                        Label("Set Recommended Kernel", systemImage: "shippingbox")
                    }
                }
            }
            .padding(6)
        } label: {
            Text("Backend").font(.headline)
        }
    }

    private var statusColor: Color {
        switch state.systemStatus {
        case .running: return .green
        case .stopped: return .red
        case .unknown: return .orange
        }
    }

    // MARK: - Disk usage

    private var diskUsageCard: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Spacer()
                    if dfLoading { ProgressView().controlSize(.small) }
                    Button {
                        Task { await loadDiskUsage() }
                    } label: {
                        Label("Refresh", systemImage: "arrow.clockwise")
                    }
                }
                if dfRows.isEmpty {
                    Text(dfLoading ? "Loading…" : "No data")
                        .foregroundStyle(.secondary)
                        .padding(.vertical, 8)
                } else {
                    Table(dfRows) {
                        TableColumn("Type", value: \.TypeName)
                        TableColumn("Total", value: \.TotalCount)
                        TableColumn("Active", value: \.Active)
                        TableColumn("Size", value: \.Size)
                        TableColumn("Reclaimable", value: \.Reclaimable)
                    }
                    .frame(minHeight: 150)
                }
            }
            .padding(6)
        } label: {
            Text("Disk Usage").font(.headline)
        }
    }

    private func loadDiskUsage() async {
        dfLoading = true
        defer { dfLoading = false }
        dfRows = (try? await DconCLI.shared.jsonLines(DFRow.self, ["system", "df", "--format", "json"])) ?? []
    }

    // MARK: - Prune

    private var pruneCard: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 10) {
                Toggle("All unused images", isOn: $pruneAllImages)
                Toggle("Volumes", isOn: $pruneVolumes)
                HStack {
                    Spacer()
                    Button(role: .destructive) {
                        showPruneConfirm = true
                    } label: {
                        Label("Prune", systemImage: "trash")
                    }
                }
            }
            .padding(6)
        } label: {
            Text("Prune").font(.headline)
        }
        .confirmDialog(pruneConfirmMessage, isPresented: $showPruneConfirm) {
            var args = ["system", "prune", "-f"]
            if pruneAllImages { args.append("-a") }
            if pruneVolumes { args.append("--volumes") }
            Task { await state.perform(args) }
        }
    }

    private var pruneConfirmMessage: String {
        var parts = ["stopped containers", "unused networks", pruneAllImages ? "all unused images" : "dangling images"]
        if pruneVolumes { parts.append("unused volumes") }
        return "This removes " + parts.joined(separator: ", ") + ". This cannot be undone."
    }

    // MARK: - Diagnostics

    private var diagnosticsCard: some View {
        GroupBox {
            HStack(spacing: 10) {
                Button("Doctor") { outputSheet = OutputSheetRequest(title: "Doctor", args: ["doctor"]) }
                Button("Info") { outputSheet = OutputSheetRequest(title: "Info", args: ["info"]) }
                Button("Version") { outputSheet = OutputSheetRequest(title: "Version", args: ["version"]) }
                Button("Backend Logs") { outputSheet = OutputSheetRequest(title: "Backend Logs", args: ["system", "logs"]) }
                Spacer()
            }
            .padding(6)
        } label: {
            Text("Diagnostics").font(.headline)
        }
    }

    // MARK: - Events

    private var eventsCard: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text(eventStream == nil ? "Stopped" : "Streaming…")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button(eventStream == nil ? "Start" : "Stop") {
                        if eventStream == nil { startEvents() } else { stopEvents() }
                    }
                }
                TextPane(text: eventLines.joined(separator: "\n"))
                    .frame(minHeight: 160)
            }
            .padding(6)
        } label: {
            Text("Events").font(.headline)
        }
    }

    private func startEvents() {
        eventLines.removeAll()
        do {
            eventStream = try DconCLI.shared.stream(["system", "events"], onLine: { line in
                eventLines.append(line)
                if eventLines.count > 500 { eventLines.removeFirst(eventLines.count - 500) }
            }, onEnd: {
                eventStream = nil
            })
        } catch {
            eventLines.append("Error: \(error.localizedDescription)")
        }
    }

    private func stopEvents() {
        eventStream?.terminate()
        eventStream = nil
    }

    // MARK: - Registry

    private var registryCard: some View {
        GroupBox {
            HStack(spacing: 10) {
                Button("Log in…") {
                    TerminalLauncher.run(dconArgs: ["login"])
                }
                Button("Log out") {
                    Task { await state.perform(["logout"]) }
                }
                Spacer()
            }
            .padding(6)
        } label: {
            Text("Registry").font(.headline)
        }
    }
}

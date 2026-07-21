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

    private let cardColumns = [GridItem(.adaptive(minimum: 340), spacing: 16, alignment: .top)]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                heroCard
                LazyVGrid(columns: cardColumns, alignment: .leading, spacing: 16) {
                    diskUsageCard
                    diagnosticsCard
                    maintenanceCard
                    eventsCard
                    registryCard
                }
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

    // MARK: - Hero status

    private var heroCard: some View {
        Group {
            if !state.runtimeAvailable {
                HStack(spacing: 16) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .font(.system(size: 30))
                        .foregroundStyle(.orange)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Runtime Not Installed")
                            .font(.title2.bold())
                        Text("Apple's container runtime powers dcon's backend.")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Link(destination: URL(string: "https://github.com/apple/container/releases")!) {
                        Label("Install Runtime", systemImage: "arrow.down.circle")
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                }
            } else {
                HStack(spacing: 16) {
                    Circle()
                        .fill(statusColor)
                        .frame(width: 14, height: 14)
                        .shadow(color: statusColor.opacity(0.5), radius: 4)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Backend \(state.systemStatus.label)")
                            .font(.title2.bold())
                        Text(state.systemStatus.isRunning
                            ? "The container backend is up and accepting commands."
                            : "Start the backend to run containers.")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    HStack(spacing: 10) {
                        Button {
                            Task { await state.perform(["system", "kernel", "set", "--recommended"]) }
                        } label: {
                            Label("Set Recommended Kernel", systemImage: "shippingbox")
                        }

                        Button {
                            if state.systemStatus.isRunning {
                                state.performDetached(["system", "stop"])
                            } else {
                                state.performDetached(["system", "start"])
                            }
                        } label: {
                            Label(state.systemStatus.isRunning ? "Stop" : "Start",
                                  systemImage: state.systemStatus.isRunning ? "stop.fill" : "play.fill")
                                .frame(minWidth: 44)
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(state.systemStatus.isRunning ? .red : .accentColor)
                    }
                }
            }
        }
        .padding(20)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardSurface()
    }

    private var statusColor: Color {
        switch state.systemStatus {
        case .running: return .green
        case .stopped: return .red
        case .unknown: return .orange
        }
    }

    // MARK: - Card header

    @ViewBuilder
    private func cardHeader(_ title: String, caption: String? = nil) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.headline)
            if let caption {
                Text(caption)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    // MARK: - Disk usage

    private var diskUsageCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top) {
                cardHeader("Disk Usage", caption: "Space used by images, containers, and volumes.")
                Spacer()
                if dfLoading { ProgressView().controlSize(.small) }
                Button {
                    Task { await loadDiskUsage() }
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                        .labelStyle(.iconOnly)
                }
                .buttonStyle(.borderless)
                .help("Refresh disk usage")
            }
            if dfRows.isEmpty {
                Text(dfLoading ? "Loading…" : "No data")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .center)
                    .padding(.vertical, 10)
            } else {
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 5) {
                    GridRow {
                        Text("Type")
                        Text("Count").gridColumnAlignment(.trailing)
                        Text("Size").gridColumnAlignment(.trailing)
                        Text("Reclaimable").gridColumnAlignment(.trailing)
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    Divider().gridCellColumns(4)
                    ForEach(dfRows) { row in
                        GridRow {
                            Text(row.TypeName)
                            Text(row.TotalCount).monospacedDigit()
                            Text(row.Size).monospacedDigit()
                            Text(row.Reclaimable).monospacedDigit().foregroundStyle(.secondary)
                        }
                        .font(.callout)
                        Divider().gridCellColumns(4)
                    }
                }
                .animation(.default, value: dfRows)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardSurface()
    }

    private func loadDiskUsage() async {
        dfLoading = true
        defer { dfLoading = false }
        dfRows = (try? await DconCLI.shared.jsonLines(DFRow.self, ["system", "df", "--format", "json"])) ?? []
    }

    // MARK: - Diagnostics

    private var diagnosticsCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            cardHeader("Diagnostics", caption: "Run backend checks and inspect logs.")
            VStack(spacing: 6) {
                diagnosticButton("Doctor", symbol: "stethoscope") {
                    outputSheet = OutputSheetRequest(title: "Doctor", args: ["doctor"])
                }
                diagnosticButton("Info", symbol: "info.circle") {
                    outputSheet = OutputSheetRequest(title: "Info", args: ["info"])
                }
                diagnosticButton("Version", symbol: "number") {
                    outputSheet = OutputSheetRequest(title: "Version", args: ["version"])
                }
                diagnosticButton("Backend Logs", symbol: "doc.text.magnifyingglass") {
                    outputSheet = OutputSheetRequest(title: "Backend Logs", args: ["system", "logs"])
                }
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardSurface()
    }

    private func diagnosticButton(_ title: String, symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Label(title, systemImage: symbol)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .buttonStyle(.bordered)
        .help(title)
    }

    // MARK: - Maintenance (prune)

    private var maintenanceCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            cardHeader("Maintenance", caption: "Reclaim disk space from unused data.")
            Toggle("All unused images", isOn: $pruneAllImages)
            Toggle("Volumes", isOn: $pruneVolumes)
            HStack {
                Spacer()
                Button(role: .destructive) {
                    showPruneConfirm = true
                } label: {
                    Label("Prune", systemImage: "trash")
                }
                .help("Reclaim disk space from unused data")
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardSurface()
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

    // MARK: - Events

    private var eventsCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top) {
                cardHeader("Events", caption: eventStream == nil ? "Stopped" : "Streaming…")
                Spacer()
                Button {
                    if eventStream == nil { startEvents() } else { stopEvents() }
                } label: {
                    Label(eventStream == nil ? "Start" : "Stop",
                          systemImage: eventStream == nil ? "play.fill" : "stop.fill")
                }
                .buttonStyle(.bordered)
                .help(eventStream == nil ? "Start streaming backend events" : "Stop streaming backend events")
            }
            if eventLines.isEmpty {
                Text(eventStream == nil ? "Start streaming to see backend events." : "Waiting for events…")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, minHeight: 160, alignment: .center)
            } else {
                LogPane(lines: eventLines)
                    .frame(minHeight: 160)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardSurface()
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
        VStack(alignment: .leading, spacing: 10) {
            cardHeader("Registry", caption: "Sign in to pull and push private images.")
            HStack(spacing: 10) {
                Button {
                    TerminalLauncher.run(dconArgs: ["login"])
                } label: {
                    Label("Log in…", systemImage: "person.crop.circle.badge.plus")
                }
                .help("Sign in to a registry")
                Button {
                    Task { await state.perform(["logout"]) }
                } label: {
                    Label("Log out", systemImage: "person.crop.circle.badge.minus")
                }
                .help("Sign out of the current registry")
                Spacer()
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardSurface()
    }
}

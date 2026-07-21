import SwiftUI

struct MainWindow: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        NavigationSplitView {
            List(selection: $state.section) {
                Section("Resources") {
                    ForEach([SidebarSection.containers, .images, .volumes, .networks]) { s in
                        sidebarRow(s)
                    }
                }
                Section("Environments") {
                    ForEach([SidebarSection.machines, .warmPool, .compose]) { s in
                        sidebarRow(s)
                    }
                }
                Section {
                    Label(SidebarSection.system.rawValue, systemImage: SidebarSection.system.symbol)
                        .tag(SidebarSection.system)
                }
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 200)
            .safeAreaInset(edge: .bottom) {
                BackendStatusFooter()
            }
        } detail: {
            detailView
                .paneStyle()
                .navigationTitle(state.section.rawValue)
                .toolbar {
                    ToolbarItemGroup(placement: .primaryAction) {
                        if state.busy {
                            ProgressView()
                                .controlSize(.small)
                                .help("Working…")
                        }
                        Button {
                            Task { await state.refreshAll() }
                        } label: {
                            Label("Refresh", systemImage: "arrow.clockwise")
                        }
                        .help("Refresh all data (⌘R)")
                    }
                }
        }
        .navigationSplitViewStyle(.balanced)
        .alert("dcon", isPresented: Binding(
            get: { state.lastError != nil },
            set: { if !$0 { state.lastError = nil } }
        )) {
            Button("OK", role: .cancel) { state.lastError = nil }
        } message: {
            Text(state.lastError ?? "")
        }
    }

    /// Sidebar row with a live item count (running/total for containers).
    /// The count is a plain trailing Text rather than `.badge`, which breaks
    /// row hit-testing in selectable sidebar lists.
    private func sidebarRow(_ s: SidebarSection) -> some View {
        HStack {
            Label(s.rawValue, systemImage: s.symbol)
            Spacer()
            if let count = countText(for: s) {
                Text(count)
                    .font(.caption)
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
            }
        }
        .contentShape(Rectangle())
        .tag(s)
    }

    private func countText(for s: SidebarSection) -> String? {
        switch s {
        case .containers where !state.containers.isEmpty:
            return "\(state.runningContainers.count)/\(state.containers.count)"
        case .images where !state.images.isEmpty:
            return "\(state.images.count)"
        case .volumes where !state.volumes.isEmpty:
            return "\(state.volumes.count)"
        case .networks where !state.networks.isEmpty:
            return "\(state.networks.count)"
        case .machines where !state.machines.isEmpty:
            return "\(state.machines.count)"
        case .warmPool where !state.warmMembers.isEmpty:
            return "\(state.warmMembers.count)"
        default:
            return nil
        }
    }

    @ViewBuilder
    private var detailView: some View {
        if !state.cliAvailable {
            MissingCLIView()
        } else if !state.initialLoadComplete {
            VStack(spacing: 12) {
                ProgressView()
                Text("Connecting to dcon…")
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            switch state.section {
            case .containers: ContainersView()
            case .images: ImagesView()
            case .volumes: VolumesView()
            case .networks: NetworksView()
            case .machines: MachinesView()
            case .warmPool: WarmPoolView()
            case .compose: ComposeView()
            case .system: SystemView()
            }
        }
    }
}

/// Sidebar footer: backend state dot + start/stop control.
struct BackendStatusFooter: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(statusColor)
                .frame(width: 9, height: 9)
            Text(statusLabel)
                .font(.callout)
                .foregroundStyle(.secondary)
            Spacer()
            if !state.runtimeAvailable {
                Link("Install Runtime", destination: URL(string: "https://github.com/apple/container/releases")!)
                    .controlSize(.small)
            } else if state.systemStatus.isRunning {
                Button("Stop") { state.performDetached(["system", "stop"]) }
                    .controlSize(.small)
            } else {
                Button("Start") { state.performDetached(["system", "start"]) }
                    .controlSize(.small)
            }
        }
        .padding(10)
        .chromeStyle()
    }

    private var statusColor: Color {
        if !state.runtimeAvailable { return .orange }
        return state.systemStatus.isRunning ? .green : .red
    }

    private var statusLabel: String {
        if !state.runtimeAvailable { return "Runtime not installed" }
        return "Backend \(state.systemStatus.label.lowercased())"
    }
}

struct MissingCLIView: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        ContentUnavailableView {
            Label("dcon CLI not found", systemImage: "exclamationmark.triangle")
        } description: {
            Text("Install the dcon CLI (or set DCON_BIN), then retry.")
        } actions: {
            Button("Retry") {
                state.cli.rediscover()
                Task { await state.refreshAll() }
            }
        }
    }
}

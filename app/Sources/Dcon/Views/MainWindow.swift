import SwiftUI

struct MainWindow: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        NavigationSplitView {
            List(selection: $state.section) {
                Section("Resources") {
                    ForEach([SidebarSection.containers, .images, .volumes, .networks]) { s in
                        Label(s.rawValue, systemImage: s.symbol).tag(s)
                    }
                }
                Section("Environments") {
                    ForEach([SidebarSection.machines, .warmPool, .compose]) { s in
                        Label(s.rawValue, systemImage: s.symbol).tag(s)
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
        }
        .alert("dcon", isPresented: Binding(
            get: { state.lastError != nil },
            set: { if !$0 { state.lastError = nil } }
        )) {
            Button("OK", role: .cancel) { state.lastError = nil }
        } message: {
            Text(state.lastError ?? "")
        }
    }

    @ViewBuilder
    private var detailView: some View {
        if !state.cliAvailable {
            MissingCLIView()
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
                .fill(state.systemStatus.isRunning ? Color.green : Color.red)
                .frame(width: 9, height: 9)
            Text("Backend \(state.systemStatus.label.lowercased())")
                .font(.callout)
                .foregroundStyle(.secondary)
            Spacer()
            if state.systemStatus.isRunning {
                Button("Stop") { state.performDetached(["system", "stop"]) }
                    .controlSize(.small)
            } else {
                Button("Start") { state.performDetached(["system", "start"]) }
                    .controlSize(.small)
            }
        }
        .padding(10)
        .background(.bar)
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

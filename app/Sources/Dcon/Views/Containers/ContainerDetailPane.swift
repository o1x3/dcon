import SwiftUI

/// Right-hand detail pane for a selected container: header + a segmented
/// picker over Logs/Inspect/Stats/Processes/Ports. The caller (ContainersView)
/// owns rename/remove confirmation state so the same dialogs work whether the
/// action came from here or from a row's context menu; give this view a fresh
/// `.id(container.id)` at the call site so switching the selection tears down
/// and rebuilds it (terminating any running log stream / stats poll cleanly).
struct ContainerDetailPane: View {
    let container: ContainerRow
    var onRename: () -> Void
    var onRemove: () -> Void
    var onForceRemove: () -> Void

    private enum Tab: String, CaseIterable, Identifiable {
        case logs = "Logs", inspect = "Inspect", stats = "Stats", processes = "Processes", ports = "Ports"
        var id: String { rawValue }
    }

    @State private var tab: Tab = .logs

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            Picker("View", selection: $tab) {
                ForEach(Tab.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding(8)
            Divider()
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .paneStyle()
        .toolbar {
            ToolbarItem {
                Menu {
                    ContainerActionButtons(
                        row: container,
                        onRename: onRename,
                        onRemove: onRemove,
                        onForceRemove: onForceRemove
                    )
                } label: {
                    Label("Actions", systemImage: "ellipsis.circle")
                }
            }
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Circle().fill(statusColor).frame(width: 10, height: 10)
                Text(container.Names).font(.title3).bold()
                Spacer()
                Text(container.State.capitalized)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Text(container.Image)
                .font(.callout)
                .foregroundStyle(.secondary)
            if !container.Ports.isEmpty {
                Text(container.Ports)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(12)
    }

    private var statusColor: Color {
        if container.isRunning { return .green }
        if container.isPaused { return .orange }
        return .gray
    }

    @ViewBuilder
    private var content: some View {
        switch tab {
        case .logs: ContainerLogsPane(containerID: container.id)
        case .inspect: ContainerInspectPane(containerID: container.id)
        case .stats: ContainerStatsPane(container: container)
        case .processes: ContainerProcessesPane(containerID: container.id)
        case .ports: ContainerPortsPane(container: container)
        }
    }
}

import SwiftUI

/// Right-hand detail pane for a selected container: header + a segmented
/// picker over Logs/Inspect/Stats/Processes/Ports. The caller (ContainersView)
/// owns rename/remove confirmation state so the same dialogs work whether the
/// action came from here or from a row's context menu; give this view a fresh
/// `.id(container.id)` at the call site so switching the selection tears down
/// and rebuilds it (terminating any running log stream / stats poll cleanly).
struct ContainerDetailPane: View {
    @EnvironmentObject var state: AppState
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
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Text(container.Names.isEmpty ? container.shortID : container.Names)
                    .font(.title2)
                    .bold()
                    .lineLimit(1)
                StatusPill(text: container.State)
                Spacer()
            }
            HStack(spacing: 6) {
                Text(container.Image)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                Text("·")
                    .foregroundStyle(.secondary)
                Text(container.shortID)
                    .font(.system(.callout, design: .monospaced))
                    .foregroundStyle(.secondary)
                CopyButton(label: "Copy ID", value: container.id)
                    .controlSize(.small)
            }
            if !container.Ports.isEmpty {
                Text(container.Ports)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            actionRow
        }
        .padding(12)
    }

    private var actionRow: some View {
        HStack(spacing: 8) {
            Button {
                perform(["start", container.id])
            } label: {
                Label("Start", systemImage: "play.fill")
            }
            .disabled(container.isRunning || container.isPaused)

            Button {
                perform(["stop", container.id])
            } label: {
                Label("Stop", systemImage: "stop.fill")
            }
            .disabled(!container.isRunning)

            Button {
                perform(["restart", container.id])
            } label: {
                Label("Restart", systemImage: "arrow.clockwise")
            }
            .disabled(!container.isRunning)

            Button {
                TerminalLauncher.run(dconArgs: ["exec", "-it", container.id, "/bin/sh"])
            } label: {
                Label("Shell", systemImage: "terminal")
            }
            .disabled(!container.isRunning)

            Button(role: .destructive, action: onRemove) {
                Label("Remove", systemImage: "trash")
            }
            .tint(.red)
            .disabled(container.isRunning || container.isPaused)
        }
        .buttonStyle(.bordered)
    }

    private func perform(_ args: [String]) {
        Task { await state.perform(args) }
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

import SwiftUI

/// OrbStack/Docker-Desktop-style containers manager: a searchable master
/// table backed by `state.containers` (auto-polled) plus a detail pane with
/// logs/inspect/stats/processes/ports for the selected container.
struct ContainersView: View {
    @EnvironmentObject var state: AppState

    @State private var selection: String?
    @State private var searchText = ""

    @State private var showRunSheet = false
    @State private var confirmPrune = false
    @State private var renameTarget: ContainerRow?
    @State private var pendingRemove: ContainerRow?
    @State private var pendingForceRemove: ContainerRow?

    private var filtered: [ContainerRow] {
        guard !searchText.isEmpty else { return state.containers }
        let q = searchText.lowercased()
        return state.containers.filter {
            $0.Names.lowercased().contains(q) || $0.Image.lowercased().contains(q) || $0.ID.lowercased().contains(q)
        }
    }

    private var selectedContainer: ContainerRow? {
        guard let selection else { return nil }
        return state.containers.first { $0.id == selection }
    }

    var body: some View {
        NavigationSplitView {
            listPane
                .navigationSplitViewColumnWidth(min: 380, ideal: 480)
        } detail: {
            detailPane
        }
        .toolbar {
            ToolbarItemGroup {
                Button {
                    showRunSheet = true
                } label: {
                    Label("Run…", systemImage: "play.fill")
                }
                Button(role: .destructive) {
                    confirmPrune = true
                } label: {
                    Label("Prune", systemImage: "trash")
                }
                RefreshButton()
            }
        }
        .sheet(isPresented: $showRunSheet) {
            ContainerRunSheet()
        }
        .sheet(item: $renameTarget) { row in
            ContainerRenameSheet(container: row)
        }
        .confirmDialog(
            "Remove all stopped containers?",
            isPresented: $confirmPrune
        ) {
            Task { await state.perform(["container", "prune"]) }
        }
        .confirmDialog(
            "Remove container \(pendingRemove?.shortID ?? "")?",
            isPresented: Binding(
                get: { pendingRemove != nil },
                set: { if !$0 { pendingRemove = nil } }
            )
        ) {
            if let row = pendingRemove {
                Task { await state.perform(["rm", row.id]) }
            }
        }
        .confirmDialog(
            "Force remove container \(pendingForceRemove?.shortID ?? "")? This stops it first if running.",
            isPresented: Binding(
                get: { pendingForceRemove != nil },
                set: { if !$0 { pendingForceRemove = nil } }
            )
        ) {
            if let row = pendingForceRemove {
                Task { await state.perform(["rm", "-f", row.id]) }
            }
        }
    }

    // MARK: - Master list

    private var listPane: some View {
        Group {
            if state.containers.isEmpty {
                EmptyListView(title: "No Containers", symbol: "shippingbox", description: "Run a container to see it here.")
            } else {
                table
            }
        }
        .searchable(text: $searchText, prompt: "Filter by name, image, or id")
    }

    private var table: some View {
        Table(filtered, selection: $selection) {
            TableColumn("Name") { row in
                HStack(spacing: 6) {
                    Circle().fill(statusColor(row)).frame(width: 8, height: 8)
                    Text(row.Names.isEmpty ? row.shortID : row.Names).lineLimit(1)
                }
            }
            TableColumn("Image", value: \.Image)
            TableColumn("Status", value: \.Status)
            TableColumn("Ports") { row in
                Text(row.Ports.isEmpty ? "–" : row.Ports).lineLimit(1)
            }
            TableColumn("Created") { row in
                Text(row.RunningFor.isEmpty ? row.CreatedAt : row.RunningFor)
            }
        }
        .contextMenu(forSelectionType: String.self) { ids in
            if let row = filtered.first(where: { ids.contains($0.id) }) {
                ContainerActionButtons(
                    row: row,
                    onRename: { renameTarget = row },
                    onRemove: { pendingRemove = row },
                    onForceRemove: { pendingForceRemove = row }
                )
            }
        } primaryAction: { ids in
            if let id = ids.first { selection = id }
        }
    }

    private func statusColor(_ row: ContainerRow) -> Color {
        if row.isRunning { return .green }
        if row.isPaused { return .orange }
        return .gray
    }

    // MARK: - Detail

    @ViewBuilder
    private var detailPane: some View {
        if let row = selectedContainer {
            ContainerDetailPane(
                container: row,
                onRename: { renameTarget = row },
                onRemove: { pendingRemove = row },
                onForceRemove: { pendingForceRemove = row }
            )
            .id(row.id)
        } else {
            EmptyListView(title: "No Container Selected", symbol: "shippingbox", description: "Select a container to view its logs, stats, and processes.")
        }
    }
}

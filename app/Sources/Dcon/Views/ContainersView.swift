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

    @State private var sortOrder: [KeyPathComparator<ContainerRow>] = [KeyPathComparator(\.Names, order: .forward)]
    @State private var sortedRows: [ContainerRow] = []

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
                .paneStyle()
        }
        .navigationSplitViewStyle(.balanced)
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
                emptyState
            } else {
                table
            }
        }
        .searchable(text: $searchText, prompt: "Filter by name, image, or id")
        .paneStyle()
    }

    private var emptyState: some View {
        ContentUnavailableView {
            Label("No Containers", systemImage: "shippingbox")
        } description: {
            Text("Run a container to see it here.")
        } actions: {
            Button {
                showRunSheet = true
            } label: {
                Label("Run a Container…", systemImage: "play.fill")
            }
            .buttonStyle(.borderedProminent)
        }
    }

    private var table: some View {
        Table(sortedRows, selection: $selection, sortOrder: $sortOrder) {
            TableColumn("Name", sortUsing: KeyPathComparator(\.Names)) { row in
                VStack(alignment: .leading, spacing: 2) {
                    Text(row.Names.isEmpty ? row.shortID : row.Names)
                        .fontWeight(.semibold)
                        .lineLimit(1)
                    Text(row.Image)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            TableColumn("Status", sortUsing: KeyPathComparator(\.Status)) { row in
                StatusPill(text: row.State)
            }
            TableColumn("Ports", sortUsing: KeyPathComparator(\.Ports)) { row in
                Text(row.Ports.isEmpty ? "–" : row.Ports).lineLimit(1)
            }
            TableColumn("Created", sortUsing: KeyPathComparator(\.CreatedAt)) { row in
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
        .onAppear { sortedRows = filtered.sorted(using: sortOrder) }
        .onChange(of: sortOrder) { _, newOrder in
            sortedRows = filtered.sorted(using: newOrder)
        }
        .onChange(of: filtered) { _, newFiltered in
            sortedRows = newFiltered.sorted(using: sortOrder)
        }
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

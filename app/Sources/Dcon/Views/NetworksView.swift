import AppKit
import SwiftUI

/// Networks section: browse container networks, create new ones, and manage
/// existing ones (inspect, connect/disconnect containers, remove).
struct NetworksView: View {
    @EnvironmentObject var state: AppState
    @State private var searchText = ""
    @State private var selection = Set<NetworkRow.ID>()
    @State private var sortOrder = [KeyPathComparator(\NetworkRow.Name)]

    @State private var showCreateSheet = false
    @State private var inspectRequest: OutputRequest?
    @State private var connectTarget: NetworkRow?
    @State private var disconnectTarget: NetworkRow?
    @State private var removeTarget: NetworkRow?
    @State private var showRemoveConfirm = false
    @State private var showPruneConfirm = false

    private var filtered: [NetworkRow] {
        guard !searchText.isEmpty else { return state.networks }
        let q = searchText.lowercased()
        return state.networks.filter {
            $0.Name.lowercased().contains(q) || $0.ID.lowercased().contains(q) || $0.Driver.lowercased().contains(q)
        }
    }

    private var sorted: [NetworkRow] {
        filtered.sorted(using: sortOrder)
    }

    var body: some View {
        VStack(spacing: 0) {
            Group {
                if state.networks.isEmpty {
                    EmptyStateView(title: "No Networks", symbol: "network",
                                   description: "Create a network to connect containers.",
                                   actionTitle: "Create Network…") { showCreateSheet = true }
                } else {
                    table
                }
            }
            Divider()
            footer
        }
        .searchable(text: $searchText, prompt: "Filter by name, ID, or driver")
        .toolbar { toolbarContent }
        .sheet(isPresented: $showCreateSheet) { CreateNetworkSheet() }
        .sheet(item: $inspectRequest) { req in InspectSheet(title: req.title, args: req.args) }
        .sheet(item: $connectTarget) { net in ConnectContainerSheet(network: net, mode: .connect) }
        .sheet(item: $disconnectTarget) { net in ConnectContainerSheet(network: net, mode: .disconnect) }
        .confirmDialog("Remove network \(removeTarget?.Name ?? "")?", isPresented: $showRemoveConfirm) {
            guard let row = removeTarget else { return }
            Task { await state.perform(["network", "rm", row.Name]) }
        }
        .confirmDialog("Remove all unused networks?", isPresented: $showPruneConfirm) {
            Task { await state.perform(["network", "prune"]) }
        }
    }

    private var table: some View {
        Table(sorted, selection: $selection, sortOrder: $sortOrder) {
            TableColumn("Name", value: \.Name) { row in
                Text(row.Name).fontWeight(.semibold)
            }
            TableColumn("Network ID", value: \.ID) { row in
                Text(String(row.ID.prefix(12)))
                    .font(.system(.body, design: .monospaced))
                    .foregroundStyle(.secondary)
            }
            TableColumn("Driver", value: \.Driver)
            TableColumn("Scope", value: \.Scope)
            TableColumn("Subnet", value: \.Subnet) { row in
                Text(row.Subnet)
                    .font(.system(.body, design: .monospaced))
                    .foregroundStyle(.secondary)
            }
        }
        .contextMenu(forSelectionType: NetworkRow.ID.self) { ids in
            contextMenuItems(for: ids)
        } primaryAction: { ids in
            if let row = filtered.first(where: { ids.contains($0.id) }) {
                inspectRequest = OutputRequest(title: "Inspect \(row.Name)", args: ["network", "inspect", row.Name])
            }
        }
    }

    @ViewBuilder
    private func contextMenuItems(for ids: Set<NetworkRow.ID>) -> some View {
        let rows = state.networks.filter { ids.contains($0.id) }
        if rows.count == 1, let row = rows.first {
            Button("Inspect") {
                inspectRequest = OutputRequest(title: "Inspect \(row.Name)", args: ["network", "inspect", row.Name])
            }
            Divider()
            Button("Connect Container…") { connectTarget = row }
            Button("Disconnect Container…") { disconnectTarget = row }
            Divider()
            CopyButton(label: "Copy ID", value: row.ID)
            Divider()
            Button("Remove", role: .destructive) {
                removeTarget = row
                showRemoveConfirm = true
            }
        }
    }

    private var toolbarContent: some ToolbarContent {
        ToolbarItemGroup {
            Button { showCreateSheet = true } label: { Label("Create…", systemImage: "plus") }
                .controlSize(.regular)
            Button { showPruneConfirm = true } label: { Label("Prune", systemImage: "trash") }
                .controlSize(.regular)
            RefreshButton()
                .controlSize(.regular)
        }
    }

    private var footer: some View {
        HStack {
            let count = filtered.count
            Text("\(count) \(count == 1 ? "network" : "networks")")
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .chromeStyle()
    }
}

/// Sheet to create a network by name.
private struct CreateNetworkSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Create Network").font(.headline)
            TextField("Name", text: $name)
                .textFieldStyle(.roundedBorder)
                .onSubmit(submit)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Create") { submit() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 420)
    }

    private func submit() {
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return }
        Task {
            await state.perform(["network", "create", trimmed])
            dismiss()
        }
    }
}

/// Sheet to connect or disconnect a container to/from a network, picked from
/// the live container list.
private struct ConnectContainerSheet: View {
    enum Mode { case connect, disconnect }

    let network: NetworkRow
    let mode: Mode
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var containerID: String?

    private var title: String {
        mode == .connect ? "Connect Container to \(network.Name)" : "Disconnect Container from \(network.Name)"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(title).font(.headline)
            if state.containers.isEmpty {
                Text("No containers available.").foregroundStyle(.secondary)
            } else {
                Picker("Container", selection: $containerID) {
                    Text("Choose…").tag(String?.none)
                    ForEach(state.containers) { c in
                        Text("\(c.Names) (\(c.shortID))").tag(Optional(c.ID))
                    }
                }
                .labelsHidden()
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button(mode == .connect ? "Connect" : "Disconnect") { submit() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(containerID == nil)
            }
        }
        .padding(20)
        .frame(width: 420)
    }

    private func submit() {
        guard let containerID else { return }
        let verb = mode == .connect ? "connect" : "disconnect"
        Task {
            await state.perform(["network", verb, network.Name, containerID])
            dismiss()
        }
    }
}

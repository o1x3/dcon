import AppKit
import SwiftUI

/// Volumes section: browse local volumes, create new ones, and manage
/// existing ones (inspect, remove, reveal on disk).
struct VolumesView: View {
    @EnvironmentObject var state: AppState
    @State private var searchText = ""
    @State private var selection = Set<VolumeRow.ID>()
    @State private var sortOrder = [KeyPathComparator(\VolumeRow.Name)]

    @State private var showCreateSheet = false
    @State private var inspectRequest: OutputRequest?
    @State private var removeTarget: VolumeRow?
    @State private var forceRemove = false
    @State private var showRemoveConfirm = false
    @State private var showPruneConfirm = false

    private var filtered: [VolumeRow] {
        guard !searchText.isEmpty else { return state.volumes }
        let q = searchText.lowercased()
        return state.volumes.filter {
            $0.Name.lowercased().contains(q) || $0.Driver.lowercased().contains(q)
        }
    }

    private var sorted: [VolumeRow] {
        filtered.sorted(using: sortOrder)
    }

    var body: some View {
        VStack(spacing: 0) {
            Group {
                if state.volumes.isEmpty {
                    EmptyStateView(title: "No Volumes", symbol: "externaldrive",
                                   description: "Create a volume to persist container data.",
                                   actionTitle: "Create Volume…") { showCreateSheet = true }
                } else if filtered.isEmpty {
                    ContentUnavailableView.search(text: searchText)
                } else {
                    table
                }
            }
            Divider()
            footer
        }
        .searchable(text: $searchText, prompt: "Filter by name or driver")
        .toolbar { toolbarContent }
        .sheet(isPresented: $showCreateSheet) { CreateVolumeSheet() }
        .sheet(item: $inspectRequest) { req in InspectSheet(title: req.title, args: req.args) }
        .confirmDialog(
            forceRemove ? "Force remove volume \(removeTarget?.Name ?? "")?" : "Remove volume \(removeTarget?.Name ?? "")?",
            isPresented: $showRemoveConfirm
        ) {
            guard let row = removeTarget else { return }
            let args = forceRemove ? ["volume", "rm", "-f", row.Name] : ["volume", "rm", row.Name]
            Task { await state.perform(args) }
        }
        .confirmDialog("Remove all unused volumes?", isPresented: $showPruneConfirm) {
            Task { await state.perform(["volume", "prune"]) }
        }
    }

    private var table: some View {
        Table(sorted, selection: $selection, sortOrder: $sortOrder) {
            TableColumn("Name", value: \.Name) { row in
                HStack(spacing: 8) {
                    IconAvatar(seed: row.Name, symbol: "externaldrive.fill", size: 24)
                    Text(row.Name)
                        .fontWeight(.semibold)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }
            .width(min: 140, ideal: 220)
            TableColumn("Driver", value: \.Driver) { row in
                Text(row.Driver).lineLimit(1)
            }
            .width(min: 70, ideal: 90)
            TableColumn("Scope", value: \.Scope) { row in
                Text(row.Scope).lineLimit(1)
            }
            .width(min: 60, ideal: 80)
            TableColumn("Mountpoint", value: \.Mountpoint) { row in
                Text(row.Mountpoint)
                    .font(.system(.body, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            .width(min: 200, ideal: 340)
        }
        .contextMenu(forSelectionType: VolumeRow.ID.self) { ids in
            contextMenuItems(for: ids)
        } primaryAction: { ids in
            if let row = filtered.first(where: { ids.contains($0.id) }) {
                inspectRequest = OutputRequest(title: "Inspect \(row.Name)", args: ["volume", "inspect", row.Name])
            }
        }
        .animation(.default, value: sorted)
    }

    @ViewBuilder
    private func contextMenuItems(for ids: Set<VolumeRow.ID>) -> some View {
        let rows = state.volumes.filter { ids.contains($0.id) }
        if rows.count == 1, let row = rows.first {
            Button("Inspect") {
                inspectRequest = OutputRequest(title: "Inspect \(row.Name)", args: ["volume", "inspect", row.Name])
            }
            Divider()
            CopyButton(label: "Copy Mountpoint", value: row.Mountpoint)
            if !row.Mountpoint.isEmpty, FileManager.default.fileExists(atPath: row.Mountpoint) {
                Button("Show in Finder") {
                    NSWorkspace.shared.selectFile(row.Mountpoint, inFileViewerRootedAtPath: "")
                }
            }
            Divider()
            Button("Remove", role: .destructive) {
                removeTarget = row
                forceRemove = false
                showRemoveConfirm = true
            }
            Button("Force Remove", role: .destructive) {
                removeTarget = row
                forceRemove = true
                showRemoveConfirm = true
            }
        }
    }

    private var toolbarContent: some ToolbarContent {
        ToolbarItemGroup {
            Button { showCreateSheet = true } label: { Label("Create…", systemImage: "plus") }
                .controlSize(.regular)
                .help("Create a new volume")
            Button { showPruneConfirm = true } label: { Label("Prune", systemImage: "trash") }
                .controlSize(.regular)
                .help("Remove all unused volumes")
        }
    }

    private var footer: some View {
        HStack {
            Text(footerText)
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .chromeStyle()
    }

    private var footerText: String {
        let total = state.volumes.count
        let count = filtered.count
        let noun = total == 1 ? "volume" : "volumes"
        return (searchText.isEmpty || count == total) ? "\(total) \(noun)" : "\(count) of \(total) \(noun)"
    }
}

/// Sheet to create a volume with an optional name (random when left blank,
/// matching `dcon volume create`).
private struct CreateVolumeSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @FocusState private var nameFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            Text("Create Volume")
                .font(.headline)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
                .chromeStyle()
            Divider()
            VStack(alignment: .leading, spacing: 12) {
                TextField("Name (optional — random if left blank)", text: $name)
                    .textFieldStyle(.roundedBorder)
                    .focused($nameFocused)
                    .onSubmit(submit)
            }
            .padding(16)
            Divider()
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Create") { submit() }
                    .keyboardShortcut(.defaultAction)
            }
            .padding(12)
            .chromeStyle()
        }
        .frame(width: 420)
        .onAppear { nameFocused = true }
    }

    private func submit() {
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        Task {
            await state.perform(trimmed.isEmpty ? ["volume", "create"] : ["volume", "create", trimmed])
            dismiss()
        }
    }
}

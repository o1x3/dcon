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
                Text(row.Name).fontWeight(.semibold)
            }
            TableColumn("Driver", value: \.Driver)
            TableColumn("Scope", value: \.Scope)
            TableColumn("Mountpoint", value: \.Mountpoint) { row in
                Text(row.Mountpoint)
                    .font(.system(.body, design: .monospaced))
                    .foregroundStyle(.secondary)
            }
        }
        .contextMenu(forSelectionType: VolumeRow.ID.self) { ids in
            contextMenuItems(for: ids)
        } primaryAction: { ids in
            if let row = filtered.first(where: { ids.contains($0.id) }) {
                inspectRequest = OutputRequest(title: "Inspect \(row.Name)", args: ["volume", "inspect", row.Name])
            }
        }
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
            Button { showPruneConfirm = true } label: { Label("Prune", systemImage: "trash") }
                .controlSize(.regular)
            RefreshButton()
                .controlSize(.regular)
        }
    }

    private var footer: some View {
        HStack {
            let count = filtered.count
            Text("\(count) \(count == 1 ? "volume" : "volumes")")
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .chromeStyle()
    }
}

/// Sheet to create a volume with an optional name (random when left blank,
/// matching `dcon volume create`).
private struct CreateVolumeSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Create Volume").font(.headline)
            TextField("Name (optional — random if left blank)", text: $name)
                .textFieldStyle(.roundedBorder)
                .onSubmit(submit)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Create") { submit() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 420)
    }

    private func submit() {
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        Task {
            await state.perform(trimmed.isEmpty ? ["volume", "create"] : ["volume", "create", trimmed])
            dismiss()
        }
    }
}

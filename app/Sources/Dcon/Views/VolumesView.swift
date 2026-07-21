import AppKit
import SwiftUI

/// Volumes section: browse local volumes, create new ones, and manage
/// existing ones (inspect, remove, reveal on disk).
struct VolumesView: View {
    @EnvironmentObject var state: AppState
    @State private var searchText = ""
    @State private var selection = Set<VolumeRow.ID>()

    @State private var showCreateSheet = false
    @State private var outputRequest: OutputRequest?
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

    var body: some View {
        Group {
            if state.volumes.isEmpty {
                EmptyListView(title: "Volumes", symbol: "externaldrive",
                              description: "Create a volume to persist container data.")
            } else {
                table
            }
        }
        .searchable(text: $searchText, prompt: "Filter by name or driver")
        .toolbar { toolbarContent }
        .sheet(isPresented: $showCreateSheet) { CreateVolumeSheet() }
        .sheet(item: $outputRequest) { req in CommandOutputSheet(title: req.title, args: req.args) }
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
        Table(filtered, selection: $selection) {
            TableColumn("Name", value: \.Name)
            TableColumn("Driver", value: \.Driver)
            TableColumn("Scope", value: \.Scope)
            TableColumn("Mountpoint", value: \.Mountpoint)
        }
        .contextMenu(forSelectionType: VolumeRow.ID.self) { ids in
            contextMenuItems(for: ids)
        } primaryAction: { ids in
            if let row = filtered.first(where: { ids.contains($0.id) }) {
                outputRequest = OutputRequest(title: "Inspect \(row.Name)", args: ["volume", "inspect", row.Name])
            }
        }
    }

    @ViewBuilder
    private func contextMenuItems(for ids: Set<VolumeRow.ID>) -> some View {
        let rows = state.volumes.filter { ids.contains($0.id) }
        if rows.count == 1, let row = rows.first {
            Button("Inspect") {
                outputRequest = OutputRequest(title: "Inspect \(row.Name)", args: ["volume", "inspect", row.Name])
            }
            Divider()
            Button("Copy Mountpoint") { copyToPasteboard(row.Mountpoint) }
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
            Button { showPruneConfirm = true } label: { Label("Prune", systemImage: "trash") }
            RefreshButton()
        }
    }

    private func copyToPasteboard(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
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

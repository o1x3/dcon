import AppKit
import SwiftUI

/// Images section: browse local images, pull/build new ones, and manage
/// existing tags (run, tag, push, save, remove).
struct ImagesView: View {
    @EnvironmentObject var state: AppState
    @State private var searchText = ""
    @State private var selection = Set<ImageRow.ID>()

    @State private var showPullSheet = false
    @State private var showBuildSheet = false
    @State private var tagTarget: ImageRow?
    @State private var outputRequest: OutputRequest?
    @State private var removeTarget: ImageRow?
    @State private var forceRemove = false
    @State private var showRemoveConfirm = false
    @State private var showPruneConfirm = false

    private var filtered: [ImageRow] {
        guard !searchText.isEmpty else { return state.images }
        let q = searchText.lowercased()
        return state.images.filter {
            $0.Repository.lowercased().contains(q) ||
                $0.Tag.lowercased().contains(q) ||
                $0.ID.lowercased().contains(q)
        }
    }

    var body: some View {
        Group {
            if state.images.isEmpty {
                EmptyListView(title: "Images", symbol: "opticaldiscdrive",
                              description: "Pull or build an image to get started.")
            } else {
                table
            }
        }
        .searchable(text: $searchText, prompt: "Filter by repository, tag, or ID")
        .toolbar { toolbarContent }
        .sheet(isPresented: $showPullSheet) { PullImageSheet() }
        .sheet(isPresented: $showBuildSheet) { BuildImageSheet() }
        .sheet(item: $tagTarget) { row in TagImageSheet(source: row) }
        .sheet(item: $outputRequest) { req in CommandOutputSheet(title: req.title, args: req.args) }
        .confirmDialog(
            forceRemove ? "Force remove \(removeTarget?.reference ?? "")?" : "Remove \(removeTarget?.reference ?? "")?",
            isPresented: $showRemoveConfirm
        ) {
            guard let row = removeTarget else { return }
            let args = forceRemove ? ["rmi", "-f", row.ID] : ["rmi", row.ID]
            Task { await state.perform(args) }
        }
        .confirmationDialog("Remove unused images?", isPresented: $showPruneConfirm, titleVisibility: .visible) {
            Button("Remove Dangling Images") {
                Task { await state.perform(["image", "prune"]) }
            }
            Button("Remove All Unused Images", role: .destructive) {
                Task { await state.perform(["image", "prune", "-a"]) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Dangling images have no tag and aren't referenced by any container. \"All unused\" also removes tagged images not used by any container.")
        }
    }

    private var table: some View {
        Table(filtered, selection: $selection) {
            TableColumn("Repository", value: \.Repository)
            TableColumn("Tag", value: \.Tag)
            TableColumn("Image ID") { row in
                Text(String(row.ID.prefix(12))).font(.system(.body, design: .monospaced))
            }
            TableColumn("Created", value: \.CreatedSince)
            TableColumn("Size", value: \.Size)
        }
        .contextMenu(forSelectionType: ImageRow.ID.self) { ids in
            contextMenuItems(for: ids)
        } primaryAction: { ids in
            if let row = filtered.first(where: { ids.contains($0.id) }) {
                outputRequest = OutputRequest(title: "Inspect \(row.reference)", args: ["image", "inspect", row.reference])
            }
        }
    }

    @ViewBuilder
    private func contextMenuItems(for ids: Set<ImageRow.ID>) -> some View {
        let rows = state.images.filter { ids.contains($0.id) }
        if rows.count == 1, let row = rows.first {
            Button("Run") { state.performDetached(["run", "-d", row.reference]) }
            Button("Tag…") { tagTarget = row }
            Button("Push") { state.performDetached(["push", row.reference]) }
            Divider()
            Button("Inspect") {
                outputRequest = OutputRequest(title: "Inspect \(row.reference)", args: ["image", "inspect", row.reference])
            }
            Button("History") {
                outputRequest = OutputRequest(title: "History \(row.reference)", args: ["history", row.reference])
            }
            Button("Save…") { saveImage(row) }
            Divider()
            Button("Copy ID") { copyToPasteboard(row.ID) }
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
            Button { showPullSheet = true } label: { Label("Pull…", systemImage: "arrow.down.circle") }
            Button { showBuildSheet = true } label: { Label("Build…", systemImage: "hammer") }
            Button { loadImage() } label: { Label("Load…", systemImage: "tray.and.arrow.down") }
            Button { showPruneConfirm = true } label: { Label("Prune", systemImage: "trash") }
            RefreshButton()
        }
    }

    private func loadImage() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.prompt = "Load"
        panel.message = "Choose a tar archive to load as an image"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        Task { await state.perform(["load", "-i", url.path]) }
    }

    private func saveImage(_ row: ImageRow) {
        let panel = NSSavePanel()
        let base = row.Repository.replacingOccurrences(of: "/", with: "_")
        let tag = row.Tag.isEmpty || row.Tag == "<none>" ? "" : "-\(row.Tag)"
        panel.nameFieldStringValue = "\(base)\(tag).tar"
        panel.prompt = "Save"
        panel.message = "Save \(row.reference) as a tar archive"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        Task { await state.perform(["save", "-o", url.path, row.reference]) }
    }

    private func copyToPasteboard(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }
}

/// Sheet to pull an image by reference, streaming `dcon pull` output live.
private struct PullImageSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var reference = ""
    @State private var pulling = false

    var body: some View {
        if pulling {
            StreamOutputSheet(title: "Pulling \(reference)", args: ["pull", reference]) {
                Task { await state.refreshAll() }
            }
        } else {
            form
        }
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Pull Image").font(.headline)
            TextField("Reference (e.g. nginx:latest)", text: $reference)
                .textFieldStyle(.roundedBorder)
                .onSubmit(startPull)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Pull") { startPull() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(reference.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 420)
    }

    private func startPull() {
        guard !reference.trimmingCharacters(in: .whitespaces).isEmpty else { return }
        pulling = true
    }
}

/// Sheet to build an image from a chosen context directory, streaming
/// `dcon build` output live.
private struct BuildImageSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var contextDir: URL?
    @State private var dockerfile: URL?
    @State private var tag = ""
    @State private var building = false

    var body: some View {
        if building, let dir = contextDir {
            StreamOutputSheet(title: "Building \(tag.isEmpty ? dir.lastPathComponent : tag)",
                               args: buildArgs(dir: dir), cwd: dir) {
                Task { await state.refreshAll() }
            }
        } else {
            form
        }
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Build Image").font(.headline)
            HStack {
                Text(contextDir?.path ?? "No context directory chosen")
                    .foregroundStyle(contextDir == nil ? .secondary : .primary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer()
                Button("Choose…") { chooseContextDir() }
            }
            TextField("Tag (e.g. myapp:latest)", text: $tag)
                .textFieldStyle(.roundedBorder)
            HStack {
                Text(dockerfile?.path ?? "Dockerfile: PATH/Dockerfile (default)")
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer()
                Button("Choose Dockerfile…") { chooseDockerfile() }
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Build") { building = true }
                    .keyboardShortcut(.defaultAction)
                    .disabled(contextDir == nil || tag.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 460)
    }

    private func buildArgs(dir: URL) -> [String] {
        var args = ["build", "-t", tag]
        if let dockerfile { args += ["-f", dockerfile.path] }
        args.append(dir.path)
        return args
    }

    private func chooseContextDir() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose"
        panel.message = "Choose the build context directory"
        if panel.runModal() == .OK { contextDir = panel.url }
    }

    private func chooseDockerfile() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose"
        panel.message = "Choose a Dockerfile (optional; defaults to PATH/Dockerfile)"
        if panel.runModal() == .OK { dockerfile = panel.url }
    }
}

/// Sheet to create a new tag pointing at an existing image.
private struct TagImageSheet: View {
    let source: ImageRow
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var newRef = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Tag \(source.reference)").font(.headline)
            TextField("New tag (e.g. myrepo/myimage:v2)", text: $newRef)
                .textFieldStyle(.roundedBorder)
                .onSubmit(submit)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Tag") { submit() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(newRef.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 420)
    }

    private func submit() {
        let ref = newRef.trimmingCharacters(in: .whitespaces)
        guard !ref.isEmpty else { return }
        Task {
            await state.perform(["tag", source.reference, ref])
            dismiss()
        }
    }
}

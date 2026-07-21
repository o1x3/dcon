import AppKit
import SwiftUI

/// Images section: browse local images, pull/build new ones, and manage
/// existing tags (run, tag, push, save, remove).
struct ImagesView: View {
    @EnvironmentObject var state: AppState
    @State private var searchText = ""
    @State private var selection = Set<ImageRow.ID>()
    @State private var sortOrder = [KeyPathComparator(\ImageRow.Repository)]

    @State private var showPullSheet = false
    @State private var showBuildSheet = false
    @State private var tagTarget: ImageRow?
    @State private var inspectRequest: OutputRequest?
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

    private var sorted: [ImageRow] {
        filtered.sorted(using: sortOrder)
    }

    var body: some View {
        VStack(spacing: 0) {
            Group {
                if state.images.isEmpty {
                    EmptyStateView(title: "No Images", symbol: "opticaldiscdrive",
                                   description: "Pull or build an image to get started.",
                                   actionTitle: "Pull an Image…") { showPullSheet = true }
                } else if filtered.isEmpty {
                    ContentUnavailableView.search(text: searchText)
                } else {
                    table
                }
            }
            Divider()
            footer
        }
        .searchable(text: $searchText, prompt: "Filter by repository, tag, or ID")
        .toolbar { toolbarContent }
        .sheet(isPresented: $showPullSheet) { PullImageSheet() }
        .sheet(isPresented: $showBuildSheet) { BuildImageSheet() }
        .sheet(item: $tagTarget) { row in TagImageSheet(source: row) }
        .sheet(item: $inspectRequest) { req in InspectSheet(title: req.title, args: req.args) }
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
            Button("Remove Dangling Images", role: .destructive) {
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
        Table(sorted, selection: $selection, sortOrder: $sortOrder) {
            TableColumn("Repository", value: \.Repository) { row in
                HStack(spacing: 6) {
                    Text(row.Repository).fontWeight(.semibold)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if !row.Tag.isEmpty, row.Tag != "<none>" {
                        Text(row.Tag)
                            .font(.caption.weight(.medium))
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(Color.accentColor.opacity(0.15), in: Capsule())
                            .foregroundStyle(Color.accentColor)
                            .lineLimit(1)
                    }
                }
            }
            .width(min: 160, ideal: 260)
            TableColumn("Image ID", value: \.ID) { row in
                Text(String(row.ID.prefix(12)))
                    .font(.system(.body, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            .width(min: 100, ideal: 120)
            TableColumn("Created", value: \.CreatedAt) { row in
                Text(row.CreatedSince)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .width(min: 90, ideal: 130)
            TableColumn("Size", value: \.sizeBytes) { row in
                Text(row.Size)
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
            }
            .width(min: 70, ideal: 90)
        }
        .contextMenu(forSelectionType: ImageRow.ID.self) { ids in
            contextMenuItems(for: ids)
        } primaryAction: { ids in
            if let row = filtered.first(where: { ids.contains($0.id) }) {
                inspectRequest = OutputRequest(title: "Inspect \(row.reference)", args: ["image", "inspect", row.reference])
            }
        }
        .animation(.default, value: sorted)
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
                inspectRequest = OutputRequest(title: "Inspect \(row.reference)", args: ["image", "inspect", row.reference])
            }
            Button("History") {
                outputRequest = OutputRequest(title: "History \(row.reference)", args: ["history", row.reference])
            }
            Button("Save…") { saveImage(row) }
            Divider()
            CopyButton(label: "Copy ID", value: row.ID)
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
                .controlSize(.regular)
                .help("Pull an image from a registry")
            Button { showBuildSheet = true } label: { Label("Build…", systemImage: "hammer") }
                .controlSize(.regular)
                .help("Build an image from a Dockerfile")
            Button { loadImage() } label: { Label("Load…", systemImage: "tray.and.arrow.down") }
                .controlSize(.regular)
                .help("Load an image from a tar archive")
            Button { showPruneConfirm = true } label: { Label("Prune", systemImage: "trash") }
                .controlSize(.regular)
                .help("Remove unused images")
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
        let total = state.images.count
        let count = filtered.count
        let noun = total == 1 ? "image" : "images"
        let countPart = (searchText.isEmpty || count == total) ? "\(total) \(noun)" : "\(count) of \(total) \(noun)"
        let totalBytes = filtered.compactMap { parseSizeToBytes($0.Size) }.reduce(0, +)
        guard totalBytes > 0 else { return countPart }
        let formatter = ByteCountFormatter()
        formatter.countStyle = .file
        return "\(countPart) · \(formatter.string(fromByteCount: Int64(totalBytes)))"
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
}

/// Parses container/Docker-style human size strings ("70.3MB", "1.2GB",
/// "0B") into raw bytes for sorting and the footer total. Returns nil for
/// anything that doesn't match the decimal-SI `HumanSizeWithPrecision`
/// format dcon itself emits (defensive, not expected in practice).
private func parseSizeToBytes(_ text: String) -> Double? {
    let trimmed = text.trimmingCharacters(in: .whitespaces)
    guard let unitStart = trimmed.firstIndex(where: { $0.isLetter }) else {
        return Double(trimmed)
    }
    guard let value = Double(trimmed[..<unitStart]) else { return nil }
    let multipliers: [String: Double] = [
        "B": 1,
        "KB": 1_000, "MB": 1_000_000, "GB": 1_000_000_000,
        "TB": 1_000_000_000_000, "PB": 1_000_000_000_000_000,
    ]
    guard let multiplier = multipliers[trimmed[unitStart...].uppercased()] else { return nil }
    return value * multiplier
}

extension ImageRow {
    /// Parsed byte count for the sortable/summable Size column; 0 when the
    /// string doesn't parse (e.g. an unexpected unit), which sorts smallest.
    var sizeBytes: Double { parseSizeToBytes(Size) ?? 0 }
}

/// Sheet to pull an image by reference, streaming `dcon pull` output live.
private struct PullImageSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var reference = ""
    @State private var pulling = false
    @FocusState private var referenceFocused: Bool

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
        VStack(spacing: 0) {
            Text("Pull Image")
                .font(.headline)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
                .chromeStyle()
            Divider()
            VStack(alignment: .leading, spacing: 12) {
                TextField("Reference (e.g. nginx:latest)", text: $reference)
                    .textFieldStyle(.roundedBorder)
                    .focused($referenceFocused)
                    .onSubmit(startPull)
            }
            .padding(16)
            Divider()
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Pull") { startPull() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(reference.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding(12)
            .chromeStyle()
        }
        .frame(width: 420)
        .onAppear { referenceFocused = true }
    }

    /// Once pulling starts, the form (and its reference field) is replaced
    /// entirely by `StreamOutputSheet` — the reference can't be edited while
    /// the pull is in flight. On success the sheet is left open (showing the
    /// full pull log) rather than auto-closing, so failures and successes
    /// both stay visible until the user dismisses it.
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
    @FocusState private var tagFocused: Bool

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
        VStack(spacing: 0) {
            Text("Build Image")
                .font(.headline)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
                .chromeStyle()
            Divider()
            VStack(alignment: .leading, spacing: 12) {
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
                    .focused($tagFocused)
                HStack {
                    Text(dockerfile?.path ?? "Dockerfile: PATH/Dockerfile (default)")
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer()
                    Button("Choose Dockerfile…") { chooseDockerfile() }
                }
            }
            .padding(16)
            Divider()
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Build") { building = true }
                    .keyboardShortcut(.defaultAction)
                    .disabled(contextDir == nil || tag.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding(12)
            .chromeStyle()
        }
        .frame(width: 460)
        .onAppear { tagFocused = true }
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
    @FocusState private var newRefFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            Text("Tag \(source.reference)")
                .font(.headline)
                .lineLimit(1)
                .truncationMode(.middle)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
                .chromeStyle()
            Divider()
            VStack(alignment: .leading, spacing: 12) {
                TextField("New tag (e.g. myrepo/myimage:v2)", text: $newRef)
                    .textFieldStyle(.roundedBorder)
                    .focused($newRefFocused)
                    .onSubmit(submit)
            }
            .padding(16)
            Divider()
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Tag") { submit() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(newRef.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding(12)
            .chromeStyle()
        }
        .frame(width: 420)
        .onAppear { newRefFocused = true }
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

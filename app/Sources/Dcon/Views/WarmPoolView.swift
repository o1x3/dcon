import SwiftUI

/// The warm pool: pre-booted single-use microVMs that let an eligible
/// `--rm` run claim a ready VM instead of paying the cold-boot cost.
struct WarmPoolView: View {
    @EnvironmentObject var state: AppState

    @State private var showWarmUpSheet = false
    @State private var showPruneAllConfirm = false
    @State private var pruneImageTarget: String?
    @State private var showPruneImageConfirm = false
    @State private var selection = Set<WarmRow.ID>()

    private var readyCount: Int {
        state.warmMembers.filter { $0.state == "ready" }.count
    }

    private var imagesCovered: Int {
        Set(state.warmMembers.map(\.image)).count
    }

    var body: some View {
        VStack(spacing: 0) {
            Group {
                if state.warmMembers.isEmpty {
                    ContentUnavailableView {
                        Label("Pool Empty", systemImage: "flame")
                    } description: {
                        Text("Pre-boot warm VMs so an eligible `dcon run --rm` starts in ~90 ms instead of cold-booting in ~700 ms.")
                    } actions: {
                        Button {
                            showWarmUpSheet = true
                        } label: {
                            Label("Warm Up…", systemImage: "flame")
                        }
                        .buttonStyle(.borderedProminent)
                    }
                } else {
                    VStack(alignment: .leading, spacing: 0) {
                        summaryRow
                        Table(state.warmMembers, selection: $selection) {
                            TableColumn("Container ID") { row in
                                Text(row.containerID)
                                    .font(.system(.body, design: .monospaced))
                                    .lineLimit(1)
                                    .truncationMode(.middle)
                                    .textSelection(.enabled)
                            }
                            .width(min: 140, ideal: 200)
                            TableColumn("Image", value: \.image)
                                .width(min: 100, ideal: 160)
                            TableColumn("Age", value: \.age)
                                .width(min: 50, ideal: 70)
                            TableColumn("State") { row in
                                StatusPill(text: row.state)
                            }
                            .width(min: 70, ideal: 90)
                            TableColumn("") { row in
                                Button(role: .destructive) {
                                    pruneImageTarget = row.image
                                    showPruneImageConfirm = true
                                } label: {
                                    Label("Prune", systemImage: "trash")
                                        .labelStyle(.iconOnly)
                                }
                                .buttonStyle(.borderless)
                                .help("Prune warm VMs for \(row.image)")
                            }
                            .width(min: 28, ideal: 28, max: 28)
                        }
                        .animation(.default, value: state.warmMembers)
                        .contextMenu(forSelectionType: WarmRow.ID.self) { ids in
                            if let row = state.warmMembers.first(where: { ids.contains($0.id) }) {
                                CopyButton(label: "Copy Container ID", value: row.containerID)
                                Button(role: .destructive) {
                                    pruneImageTarget = row.image
                                    showPruneImageConfirm = true
                                } label: {
                                    Label("Prune Warm VMs for \(row.image)", systemImage: "trash")
                                }
                            }
                        } primaryAction: { ids in
                            // Double-click copies the container id — there's nothing to
                            // "open" on a single-use warm VM, and it mirrors the
                            // per-row copy affordance already in the context menu.
                            if let row = state.warmMembers.first(where: { ids.contains($0.id) }) {
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(row.containerID, forType: .string)
                            }
                        }
                    }
                }
            }
            if !state.warmMembers.isEmpty {
                Divider()
                Text("Warm VMs are pre-booted single-use microVMs (~35 MB idle each). An eligible `dcon run --rm` claims one in ~90 ms instead of cold-booting in ~700 ms; each is destroyed after one use and the pool tops itself back up.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .padding(12)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .navigationTitle("Warm Pool")
        .toolbar {
            ToolbarItemGroup {
                Button(role: .destructive) {
                    showPruneAllConfirm = true
                } label: {
                    Label("Prune All", systemImage: "trash")
                }
                .disabled(state.warmMembers.isEmpty)
                .help("Tear down the entire warm pool")
                Button {
                    showWarmUpSheet = true
                } label: {
                    Label("Warm Up…", systemImage: "flame")
                }
                .help("Pre-boot warm VMs")
            }
        }
        .sheet(isPresented: $showWarmUpSheet) {
            WarmUpSheet()
        }
        .confirmDialog("Tear down the entire warm pool?", isPresented: $showPruneAllConfirm) {
            Task { await state.perform(["warm", "prune"]) }
        }
        .confirmDialog(
            "Prune warm VMs for \(pruneImageTarget ?? "")?",
            isPresented: $showPruneImageConfirm
        ) {
            if let image = pruneImageTarget {
                Task { await state.perform(["warm", "prune", image]) }
            }
        }
    }

    private var summaryRow: some View {
        HStack(spacing: 12) {
            StatTile(label: "Ready VMs", value: "\(readyCount)", symbol: "checkmark.circle")
            StatTile(label: "Images Covered", value: "\(imagesCovered)", symbol: "square.stack.3d.up")
            Spacer()
        }
        .padding(12)
    }
}

/// Sheet for `warm -n COUNT IMAGE`, suggesting images already known to the
/// backend so the user doesn't have to retype a reference by hand.
private struct WarmUpSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss

    @State private var image = ""
    @State private var count = 1
    @FocusState private var imageFieldFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            Text("Warm Up")
                .font(.headline)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
                .chromeStyle()
            Divider()

            VStack(alignment: .leading, spacing: 16) {
                TextField("Image (e.g. alpine, python:3.12)", text: $image)
                    .textFieldStyle(.roundedBorder)
                    .focused($imageFieldFocused)

                if !state.images.isEmpty {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 6) {
                            ForEach(state.images.prefix(8)) { img in
                                Button(img.reference) { image = img.reference }
                                    .buttonStyle(.bordered)
                                    .controlSize(.small)
                                    .help("Use \(img.reference)")
                            }
                        }
                    }
                }

                Stepper("Count: \(count)", value: $count, in: 1...8)
            }
            .padding(20)

            Divider()
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Warm Up") {
                    state.performDetached(["warm", "-n", String(count), trimmedImage])
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(trimmedImage.isEmpty)
            }
            .padding(12)
            .chromeStyle()
        }
        .frame(minWidth: 420, idealWidth: 420)
        .task { imageFieldFocused = true }
    }

    private var trimmedImage: String { image.trimmingCharacters(in: .whitespaces) }
}

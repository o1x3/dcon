import SwiftUI

/// The warm pool: pre-booted single-use microVMs that let an eligible
/// `--rm` run claim a ready VM instead of paying the cold-boot cost.
struct WarmPoolView: View {
    @EnvironmentObject var state: AppState

    @State private var showWarmUpSheet = false
    @State private var showPruneAllConfirm = false
    @State private var pruneImageTarget: String?
    @State private var showPruneImageConfirm = false

    var body: some View {
        VStack(spacing: 0) {
            Group {
                if state.warmMembers.isEmpty {
                    EmptyListView(
                        title: "Pool Empty",
                        symbol: "flame",
                        description: "Pre-boot warm VMs so eligible `--rm` runs start instantly."
                    )
                } else {
                    Table(state.warmMembers) {
                        TableColumn("Container ID", value: \.containerID)
                        TableColumn("Image", value: \.image)
                        TableColumn("Age", value: \.age)
                        TableColumn("State") { row in
                            HStack(spacing: 6) {
                                Circle()
                                    .fill(row.state == "ready" ? Color.green : Color.red)
                                    .frame(width: 8, height: 8)
                                Text(row.state)
                            }
                        }
                        TableColumn("") { row in
                            Button {
                                pruneImageTarget = row.image
                                showPruneImageConfirm = true
                            } label: {
                                Image(systemName: "trash")
                            }
                            .buttonStyle(.borderless)
                            .help("Prune warm VMs for \(row.image)")
                        }
                        .width(28)
                    }
                }
            }
            Divider()
            Text("Warm VMs are pre-booted single-use microVMs (~35 MB idle each). An eligible `dcon run --rm` claims one in ~90 ms instead of cold-booting in ~700 ms; each is destroyed after one use and the pool tops itself back up.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .padding(12)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .navigationTitle("Warm Pool")
        .toolbar {
            ToolbarItemGroup {
                RefreshButton()
                Button(role: .destructive) {
                    showPruneAllConfirm = true
                } label: {
                    Label("Prune All", systemImage: "trash")
                }
                .disabled(state.warmMembers.isEmpty)
                Button {
                    showWarmUpSheet = true
                } label: {
                    Label("Warm Up…", systemImage: "flame")
                }
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
}

/// Sheet for `warm -n COUNT IMAGE`, suggesting images already known to the
/// backend so the user doesn't have to retype a reference by hand.
private struct WarmUpSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss

    @State private var image = ""
    @State private var count = 1

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Warm Up").font(.headline)

            TextField("Image (e.g. alpine, python:3.12)", text: $image)
                .textFieldStyle(.roundedBorder)

            if !state.images.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 6) {
                        ForEach(state.images.prefix(8)) { img in
                            Button(img.reference) { image = img.reference }
                                .buttonStyle(.bordered)
                                .controlSize(.small)
                        }
                    }
                }
            }

            Stepper("Count: \(count)", value: $count, in: 1...8)

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Warm Up") {
                    state.performDetached(["warm", "-n", String(count), trimmedImage])
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(trimmedImage.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 420)
    }

    private var trimmedImage: String { image.trimmingCharacters(in: .whitespaces) }
}

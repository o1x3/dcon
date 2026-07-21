import SwiftUI

/// `dcon top CONTAINER` output, refreshable.
struct ContainerProcessesPane: View {
    let containerID: String

    @State private var output = ""
    @State private var loading = true

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Spacer()
                Button {
                    Task { await load() }
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }
            .padding(8)
            .chromeStyle()
            Divider()
            TextPane(text: loading ? "Loading…" : output)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task { await load() }
    }

    private func load() async {
        loading = true
        do {
            output = try await DconCLI.shared.capture(["top", containerID])
        } catch {
            output = error.localizedDescription
        }
        loading = false
    }
}

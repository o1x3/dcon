import SwiftUI

/// One-shot `dcon inspect CONTAINER` view, refreshable, with a Copy button.
struct ContainerInspectPane: View {
    let containerID: String

    @State private var output = "Loading…"

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Spacer()
                Button("Copy") { copyToPasteboard(output) }
                Button {
                    Task { await load() }
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }
            .padding(8)
            .chromeStyle()
            Divider()
            TextPane(text: output)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task { await load() }
    }

    private func load() async {
        do {
            output = try await DconCLI.shared.capture(["inspect", containerID])
        } catch {
            output = error.localizedDescription
        }
    }
}

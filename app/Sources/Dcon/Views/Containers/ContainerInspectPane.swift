import SwiftUI

/// One-shot `dcon inspect CONTAINER` view, refreshable, rendered as a
/// structured JSON tree (with a raw-text fallback) via `JSONInspectorView`.
struct ContainerInspectPane: View {
    let containerID: String

    @State private var output = ""
    @State private var loaded = false

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
            if loaded {
                JSONInspectorView(jsonText: output)
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
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
        loaded = true
    }
}

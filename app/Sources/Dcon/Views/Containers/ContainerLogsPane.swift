import SwiftUI

/// Live-following log view (`dcon logs --follow -n 500 CONTAINER`). Lines are
/// capped at ~5000 to bound memory on chatty containers. The stream is torn
/// down on disappear (parent gives this pane a fresh `.id` per selection, so
/// switching containers naturally terminates the old stream).
struct ContainerLogsPane: View {
    let containerID: String

    @State private var lines: [String] = []
    @State private var handle: StreamHandle?
    @State private var isFollowing = false

    private static let lineCap = 5000

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Button(isFollowing ? "Pause" : "Resume") { toggleFollow() }
                Button("Clear") { lines.removeAll() }
                Spacer()
                Text("\(lines.count) lines")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(8)
            Divider()
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 1) {
                        ForEach(Array(lines.enumerated()), id: \.offset) { idx, line in
                            Text(line)
                                .font(.system(.caption, design: .monospaced))
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .id(idx)
                        }
                    }
                    .padding(8)
                }
                .background(Color(nsColor: .textBackgroundColor))
                .onChange(of: lines.count) { _, newCount in
                    guard newCount > 0 else { return }
                    proxy.scrollTo(newCount - 1, anchor: .bottom)
                }
            }
        }
        .onAppear { startFollowing() }
        .onDisappear { stopFollowing() }
    }

    private func startFollowing() {
        handle?.terminate()
        do {
            handle = try DconCLI.shared.stream(["logs", "--follow", "-n", "500", containerID]) { line in
                lines.append(line)
                if lines.count > Self.lineCap {
                    lines.removeFirst(lines.count - Self.lineCap)
                }
            }
            isFollowing = true
        } catch {
            lines.append("[dcon: failed to start log stream — \(error.localizedDescription)]")
            isFollowing = false
        }
    }

    private func stopFollowing() {
        handle?.terminate()
        handle = nil
        isFollowing = false
    }

    private func toggleFollow() {
        if isFollowing {
            stopFollowing()
        } else {
            startFollowing()
        }
    }
}

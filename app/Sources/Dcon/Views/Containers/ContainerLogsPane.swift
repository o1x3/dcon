import AppKit
import SwiftUI

/// Live-following log view (`dcon logs --follow -n 500 CONTAINER`). Lines are
/// capped at ~5000 to bound memory on chatty containers. The stream is torn
/// down on disappear (parent gives this pane a fresh `.id` per selection, so
/// switching containers naturally terminates the old stream).
///
/// The filter field only narrows what's *displayed* — the underlying buffer
/// (and its 5000-line cap) is untouched, so Export… always writes the full
/// unfiltered log.
struct ContainerLogsPane: View {
    let containerID: String

    @State private var lines: [String] = []
    @State private var handle: StreamHandle?
    @State private var isFollowing = false
    @State private var filterText = ""
    @State private var autoscroll = true

    private static let lineCap = 5000

    private var filteredLines: [String] {
        guard !filterText.isEmpty else { return lines }
        let q = filterText.lowercased()
        return lines.filter { $0.lowercased().contains(q) }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Button {
                    toggleFollow()
                } label: {
                    Label(isFollowing ? "Pause" : "Resume",
                          systemImage: isFollowing ? "pause.fill" : "play.fill")
                        .labelStyle(.iconOnly)
                }
                .help(isFollowing ? "Pause following logs" : "Resume following logs")

                Button {
                    lines.removeAll()
                } label: {
                    Label("Clear", systemImage: "xmark.bin")
                        .labelStyle(.iconOnly)
                }
                .help("Clear the log buffer")

                HStack(spacing: 4) {
                    Image(systemName: "magnifyingglass")
                        .foregroundStyle(.secondary)
                    TextField("Filter", text: $filterText)
                        .textFieldStyle(.plain)
                    if !filterText.isEmpty {
                        Button {
                            filterText = ""
                        } label: {
                            Label("Clear Filter", systemImage: "xmark.circle.fill")
                                .labelStyle(.iconOnly)
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(.secondary)
                        .help("Clear filter")
                    }
                }
                .padding(.horizontal, 6)
                .padding(.vertical, 3)
                .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 6))
                .frame(minWidth: 100, maxWidth: 260)

                Toggle(isOn: $autoscroll) {
                    Label("Autoscroll", systemImage: "arrow.down.to.line")
                        .labelStyle(.iconOnly)
                }
                .toggleStyle(.button)
                .help(autoscroll ? "Autoscroll on — click to disable" : "Autoscroll off — click to follow new lines")

                Spacer(minLength: 4)

                Text(filterText.isEmpty ? "\(lines.count) lines" : "\(filteredLines.count)/\(lines.count)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
                    .lineLimit(1)
                    .fixedSize()

                Button {
                    exportLogs()
                } label: {
                    Label("Export…", systemImage: "square.and.arrow.up")
                        .labelStyle(.iconOnly)
                }
                .help("Export the full log buffer to a file")
            }
            .buttonStyle(.borderless)
            .controlSize(.small)
            .padding(.horizontal, 8)
            .padding(.vertical, 6)
            .chromeStyle()
            Divider()
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 1) {
                        ForEach(Array(filteredLines.enumerated()), id: \.offset) { idx, line in
                            LogLineView(line: line)
                                .id(idx)
                        }
                    }
                    .padding(8)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .contentSurface()
                .onChange(of: filteredLines.count) { _, newCount in
                    guard autoscroll, newCount > 0 else { return }
                    proxy.scrollTo(newCount - 1, anchor: .bottom)
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
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

    /// Writes the full (unfiltered) log buffer to a user-chosen file.
    private func exportLogs() {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "\(String(containerID.prefix(12)))-logs.txt"
        panel.prompt = "Export"
        panel.message = "Export the full log buffer"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            try lines.joined(separator: "\n").write(to: url, atomically: true, encoding: .utf8)
        } catch {
            lines.append("[dcon: failed to export logs — \(error.localizedDescription)]")
        }
    }

    private func toggleFollow() {
        if isFollowing {
            stopFollowing()
        } else {
            startFollowing()
        }
    }
}

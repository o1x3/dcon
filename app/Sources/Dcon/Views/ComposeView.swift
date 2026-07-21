import AppKit
import SwiftUI
import UniformTypeIdentifiers

/// Compose project runner: pick one or more `compose.yaml` files, then bring
/// each project up/down, watch `ps`, and tail aggregated logs — a thin GUI
/// over `dcon compose -f <file> ...`.
struct ComposeView: View {
    @AppStorage("composeRecents") private var recentsData = Data()
    @State private var selected: String?
    @State private var hoveredRecent: String?

    var body: some View {
        HSplitView {
            recentsList
                .frame(minWidth: 220, idealWidth: 260, maxWidth: 360)
                .paneStyle()
            detailArea
                .frame(minWidth: 420, maxWidth: .infinity, maxHeight: .infinity)
                .paneStyle()
        }
        .navigationTitle("Compose")
        .toolbar {
            ToolbarItem {
                Button {
                    openFile()
                } label: {
                    Label("Open…", systemImage: "folder.badge.plus")
                }
            }
        }
    }

    private var recentsList: some View {
        List(selection: $selected) {
            ForEach(recents, id: \.self) { path in
                recentRow(path)
                    .tag(path)
                    .contextMenu {
                        Button("Remove from Recents", role: .destructive) {
                            removeRecent(path)
                        }
                    }
            }
        }
        .overlay {
            if recents.isEmpty {
                EmptyListView(
                    title: "No Compose Projects",
                    symbol: "square.stack.3d.up",
                    description: "Open a compose.yaml to manage its services."
                )
            }
        }
    }

    private func recentRow(_ path: String) -> some View {
        HStack(spacing: 10) {
            Image(systemName: "doc.text")
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(displayName(path))
                    .font(.headline)
                    .lineLimit(1)
                Text(parentDirectory(path))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer(minLength: 8)
            if hoveredRecent == path {
                Button {
                    removeRecent(path)
                } label: {
                    Image(systemName: "xmark.circle.fill")
                }
                .buttonStyle(.borderless)
                .foregroundStyle(.secondary)
                .help("Remove from Recents")
            }
        }
        .padding(.vertical, 2)
        .contentShape(Rectangle())
        .help(path)
        .onHover { hovering in
            hoveredRecent = hovering ? path : (hoveredRecent == path ? nil : hoveredRecent)
        }
    }

    @ViewBuilder
    private var detailArea: some View {
        if let selected, recents.contains(selected) {
            ComposeProjectPane(path: selected, onRemove: {
                removeRecent(selected)
                self.selected = nil
            })
            .id(selected)
        } else {
            ContentUnavailableView {
                Label("Open a Compose File to Get Started", systemImage: "square.stack.3d.up")
            } actions: {
                Button {
                    openFile()
                } label: {
                    Label("Open…", systemImage: "folder.badge.plus")
                }
                .buttonStyle(.borderedProminent)
            }
        }
    }

    // MARK: - Recents persistence

    private var recents: [String] {
        (try? JSONDecoder().decode([String].self, from: recentsData)) ?? []
    }

    private func setRecents(_ paths: [String]) {
        recentsData = (try? JSONEncoder().encode(paths)) ?? Data()
    }

    private func removeRecent(_ path: String) {
        setRecents(recents.filter { $0 != path })
    }

    private func displayName(_ path: String) -> String {
        let url = URL(fileURLWithPath: path)
        return url.lastPathComponent
    }

    private func parentDirectory(_ path: String) -> String {
        URL(fileURLWithPath: path).deletingLastPathComponent().path
    }

    private func openFile() {
        let panel = NSOpenPanel()
        var types: [UTType] = []
        if let yaml = UTType(filenameExtension: "yaml") { types.append(yaml) }
        if let yml = UTType(filenameExtension: "yml") { types.append(yml) }
        panel.allowedContentTypes = types
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            let path = url.path
            var list = recents
            list.removeAll { $0 == path }
            list.insert(path, at: 0)
            setRecents(list)
            selected = path
        }
    }
}

/// One compose project's control pane: lifecycle buttons, a `ps` status
/// panel (auto-refreshing), and a streaming output/log panel. Only one
/// stream (an operation's output, or `logs --follow`) runs at a time.
private struct ComposeProjectPane: View {
    let path: String
    let onRemove: () -> Void

    @State private var activeStream: StreamHandle?
    @State private var streamLabel = ""
    @State private var streamLines: [String] = []
    @State private var followingLogs = false

    @State private var psOutput = ""

    private var cwd: URL { URL(fileURLWithPath: path).deletingLastPathComponent() }
    private var fileArgs: [String] { ["compose", "-f", path] }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
                .padding(16)
            operationsRow
            Divider()
            HSplitView {
                statusPane.frame(minWidth: 320)
                logPane.frame(minWidth: 320)
            }
        }
        .task { await refreshStatusLoop() }
        .onDisappear {
            activeStream?.terminate()
            activeStream = nil
        }
    }

    private var header: some View {
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                Text(URL(fileURLWithPath: path).lastPathComponent).font(.title3.bold())
                Text(path).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            Button("Remove from Recents", role: .destructive, action: onRemove)
        }
    }

    private var operationsRow: some View {
        HStack(spacing: 8) {
            Button {
                startOperation(label: "up -d", extra: ["up", "-d"])
            } label: {
                Label("Up", systemImage: "play.fill")
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.small)

            Button {
                startOperation(label: "down", extra: ["down"])
            } label: {
                Label("Down", systemImage: "poweroff")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)

            Button {
                startOperation(label: "pull", extra: ["pull"])
            } label: {
                Label("Pull", systemImage: "arrow.down.circle")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)

            Button {
                startOperation(label: "build", extra: ["build"])
            } label: {
                Label("Build", systemImage: "hammer")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)

            Button {
                startOperation(label: "restart", extra: ["restart"])
            } label: {
                Label("Restart", systemImage: "arrow.clockwise")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)

            Button {
                startOperation(label: "stop", extra: ["stop"])
            } label: {
                Label("Stop", systemImage: "stop.fill")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)

            Spacer()
            if activeStream != nil {
                ProgressView().controlSize(.small)
                Text(streamLabel).font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(10)
        .chromeStyle()
    }

    private var statusPane: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("Status").font(.headline)
                Spacer()
                Button {
                    Task { await refreshStatus() }
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }
            .padding(8)
            .chromeStyle()
            Divider()
            TextPane(text: psOutput)
        }
        .frame(maxHeight: .infinity)
    }

    private var logPane: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("Logs").font(.headline)
                Spacer()
                Button {
                    if followingLogs {
                        stopStream()
                    } else {
                        startFollowLogs()
                    }
                } label: {
                    Label(followingLogs ? "Stop" : "Follow", systemImage: followingLogs ? "stop.fill" : "play.fill")
                }
            }
            .padding(8)
            .chromeStyle()
            Divider()
            TextPane(text: streamLines.joined(separator: "\n"))
        }
        .frame(maxHeight: .infinity)
    }

    // MARK: - Streaming (operations + follow logs share one slot)

    private func startOperation(label: String, extra: [String]) {
        stopStream()
        streamLabel = label
        streamLines.removeAll()
        followingLogs = false
        do {
            activeStream = try DconCLI.shared.stream(fileArgs + extra, cwd: cwd, onLine: { line in
                streamLines.append(line)
                capLines()
            }, onEnd: {
                activeStream = nil
                Task { await refreshStatus() }
            })
        } catch {
            streamLines.append("Error: \(error.localizedDescription)")
        }
    }

    private func startFollowLogs() {
        stopStream()
        streamLabel = "logs --follow"
        streamLines.removeAll()
        followingLogs = true
        do {
            activeStream = try DconCLI.shared.stream(fileArgs + ["logs", "--follow"], cwd: cwd, onLine: { line in
                streamLines.append(line)
                capLines()
            }, onEnd: {
                activeStream = nil
                followingLogs = false
            })
        } catch {
            streamLines.append("Error: \(error.localizedDescription)")
            followingLogs = false
        }
    }

    private func stopStream() {
        activeStream?.terminate()
        activeStream = nil
        followingLogs = false
    }

    private func capLines() {
        if streamLines.count > 2000 {
            streamLines.removeFirst(streamLines.count - 2000)
        }
    }

    // MARK: - Status polling

    private func refreshStatus() async {
        psOutput = (try? await DconCLI.shared.capture(fileArgs + ["ps"], cwd: cwd)) ?? psOutput
    }

    private func refreshStatusLoop() async {
        await refreshStatus()
        while !Task.isCancelled {
            try? await Task.sleep(nanoseconds: 5_000_000_000)
            if Task.isCancelled { return }
            await refreshStatus()
        }
    }
}

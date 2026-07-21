import SwiftUI

/// Sheet that runs a dcon command and streams its output live (pull, build).
/// The process starts on appear; "Stop"/"Close" terminates it if still
/// running. `onFinish` runs exactly once, whether the stream ends naturally
/// or is interrupted, so callers can refresh app state afterwards.
struct StreamOutputSheet: View {
    let title: String
    let args: [String]
    var cwd: URL?
    var onFinish: () -> Void = {}

    @Environment(\.dismiss) private var dismiss
    @State private var lines: [String] = []
    @State private var handle: StreamHandle?
    @State private var finished = false
    @State private var didFinish = false

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                if !finished {
                    ProgressView().controlSize(.small)
                }
                Text(title).font(.headline).lineLimit(1).truncationMode(.middle)
                Spacer()
                Button(finished ? "Close" : "Stop") {
                    handle?.terminate()
                    finish()
                    dismiss()
                }
            }
            .padding(12)
            .chromeStyle()
            Divider()
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 1) {
                        ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                            Text(line.isEmpty ? " " : line)
                                .font(.system(.body, design: .monospaced))
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        Color.clear.frame(height: 1).id("bottom")
                    }
                    .padding(8)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .contentSurface()
                .onChange(of: lines.count) { _, _ in
                    withAnimation(.easeOut(duration: 0.15)) {
                        proxy.scrollTo("bottom", anchor: .bottom)
                    }
                }
            }
        }
        .paneStyle()
        .frame(minWidth: 640, minHeight: 440)
        .task {
            do {
                handle = try DconCLI.shared.stream(args, cwd: cwd, onLine: { line in
                    lines.append(line)
                }, onEnd: {
                    finished = true
                    finish()
                })
            } catch {
                lines.append(error.localizedDescription)
                finished = true
                finish()
            }
        }
        .onDisappear {
            handle?.terminate()
            finish()
        }
        .onExitCommand {
            // ⎋ only closes the sheet once the stream has finished; while a
            // pull/build is still running, escape must not silently kill it
            // the way clicking "Stop" would — the user has to use the
            // explicit button for that.
            if finished { dismiss() }
        }
    }

    /// Idempotent: the stream can end (onEnd), be stopped, and be dismissed
    /// in any order, but callers must only be notified once.
    private func finish() {
        guard !didFinish else { return }
        didFinish = true
        onFinish()
    }
}

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
            HStack {
                Text(title).font(.headline)
                Spacer()
                if !finished {
                    ProgressView().controlSize(.small)
                }
                Button(finished ? "Close" : "Stop") {
                    handle?.terminate()
                    finish()
                    dismiss()
                }
            }
            .padding(12)
            Divider()
            TextPane(text: lines.joined(separator: "\n"))
        }
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
    }

    /// Idempotent: the stream can end (onEnd), be stopped, and be dismissed
    /// in any order, but callers must only be notified once.
    private func finish() {
        guard !didFinish else { return }
        didFinish = true
        onFinish()
    }
}

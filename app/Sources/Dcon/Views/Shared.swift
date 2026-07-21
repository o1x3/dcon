import SwiftUI

/// Monospaced, selectable, scrollable text pane for inspect output, logs, etc.
struct TextPane: View {
    let text: String

    var body: some View {
        ScrollView([.vertical, .horizontal]) {
            Text(text.isEmpty ? " " : text)
                .font(.system(.body, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(8)
        }
        .background(Color(nsColor: .textBackgroundColor))
    }
}

/// Sheet that runs a dcon command on appear and shows its output (inspect,
/// history, info, ...).
struct CommandOutputSheet: View {
    let title: String
    let args: [String]
    @Environment(\.dismiss) private var dismiss
    @State private var output = "Loading…"

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text(title).font(.headline)
                Spacer()
                Button("Copy") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(output, forType: .string)
                }
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            }
            .padding(12)
            Divider()
            TextPane(text: output)
        }
        .frame(minWidth: 640, minHeight: 440)
        .task {
            do {
                output = try await DconCLI.shared.capture(args)
            } catch {
                output = error.localizedDescription
            }
        }
    }
}

/// Toolbar-friendly refresh button.
struct RefreshButton: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        Button {
            Task { await state.refreshAll() }
        } label: {
            Label("Refresh", systemImage: "arrow.clockwise")
        }
        .help("Refresh")
    }
}

/// Standard empty-list placeholder.
struct EmptyListView: View {
    let title: String
    let symbol: String
    var description: String = ""

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: symbol)
        } description: {
            Text(description)
        }
    }
}

extension View {
    /// Destructive action with a confirmation dialog.
    func confirmDialog(_ title: String, isPresented: Binding<Bool>, action: @escaping () -> Void) -> some View {
        confirmationDialog(title, isPresented: isPresented) {
            Button("Confirm", role: .destructive, action: action)
            Button("Cancel", role: .cancel) {}
        }
    }
}

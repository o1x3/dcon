import SwiftUI

/// Row action buttons (start/stop/restart/kill/pause/unpause/shell/rename/copy/
/// remove), shared between the containers table's per-row context menu and the
/// detail pane's toolbar menu. Simple lifecycle actions run immediately via
/// `state.perform`; rename and the two destructive removes are deferred to the
/// caller (`onRename`/`onRemove`/`onForceRemove`) so a single confirmation
/// sheet/dialog can live at the ContainersView level regardless of which
/// surface (table row or detail toolbar) triggered it.
///
/// Note: `pause`/`unpause` and `rename` are accepted by dcon but always fail
/// ("... is not supported by the container backend", see cmd/lifecycle.go) —
/// the failure surfaces through the existing `state.lastError` alert, same as
/// the CLI. They stay enabled here for Docker UI parity rather than being
/// silently hidden.
struct ContainerActionButtons: View {
    @EnvironmentObject var state: AppState
    let row: ContainerRow
    var onRename: () -> Void
    var onRemove: () -> Void
    var onForceRemove: () -> Void

    var body: some View {
        Group {
            if row.isRunning {
                Button("Stop") { perform(["stop", row.id]) }
                Button("Restart") { perform(["restart", row.id]) }
                Button("Kill", role: .destructive) { perform(["kill", row.id]) }
                Button("Pause") { perform(["pause", row.id]) }
                Button("Open Shell") {
                    TerminalLauncher.run(dconArgs: ["exec", "-it", row.id, "/bin/sh"])
                }
            } else if row.isPaused {
                Button("Unpause") { perform(["unpause", row.id]) }
                Button("Kill", role: .destructive) { perform(["kill", row.id]) }
            } else {
                Button("Start") { perform(["start", row.id]) }
            }
            Divider()
            Button("Rename…", action: onRename)
            Button("Copy ID") { copyToPasteboard(row.id) }
            Button("Copy Name") { copyToPasteboard(row.Names.isEmpty ? row.shortID : row.Names) }
            Divider()
            Button("Remove", role: .destructive, action: onRemove)
                .disabled(row.isRunning || row.isPaused)
            Button("Force Remove", role: .destructive, action: onForceRemove)
        }
    }

    private func perform(_ args: [String]) {
        Task { await state.perform(args) }
    }
}

/// Copies text to the system pasteboard.
func copyToPasteboard(_ text: String) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(text, forType: .string)
}

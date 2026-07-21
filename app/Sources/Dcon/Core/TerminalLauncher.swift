import Foundation
import AppKit

/// Opens interactive CLI sessions (exec -it, machine shell) in Terminal.app —
/// a real PTY beats reimplementing a terminal emulator.
enum TerminalLauncher {
    /// Open Terminal and run `dcon <args>` in a new window/tab.
    static func run(dconArgs: [String]) {
        guard let bin = DconCLI.shared.binaryURL else { return }
        let command = ([shellQuote(bin.path)] + dconArgs.map(shellQuote)).joined(separator: " ")
        let script = """
        tell application "Terminal"
            activate
            do script "\(appleScriptEscape(command))"
        end tell
        """
        DispatchQueue.global(qos: .userInitiated).async {
            var error: NSDictionary?
            NSAppleScript(source: script)?.executeAndReturnError(&error)
            if let error {
                NSLog("TerminalLauncher error: \(error)")
            }
        }
    }

    static func shellQuote(_ s: String) -> String {
        if s.range(of: "^[A-Za-z0-9_./:=@-]+$", options: .regularExpression) != nil { return s }
        return "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    static func appleScriptEscape(_ s: String) -> String {
        s.replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
    }
}

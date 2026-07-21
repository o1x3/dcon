import SwiftUI

/// Renders one log line with color: honors ANSI SGR escape codes when present,
/// otherwise applies level heuristics (error → red, warn → orange, debug/trace
/// → dimmed). Used by container logs, compose logs, events, and stream sheets.
enum LogStyler {
    /// Regex fragments that classify an uncolored line by log level.
    private static let errorHints = ["error", "fatal", "panic", "fail"]
    private static let warnHints = ["warn", "warning"]
    private static let dimHints = ["debug", "trace"]

    static func styled(_ raw: String) -> Text {
        if raw.contains("\u{1B}[") {
            return ansiText(raw)
        }
        let lower = raw.lowercased()
        // Match klog/logrus-style "level=warning", bracketed "[ERROR]", or
        // leading severity words — not any random occurrence mid-message.
        if matches(lower, hints: errorHints) { return Text(raw).foregroundStyle(.red) }
        if matches(lower, hints: warnHints) { return Text(raw).foregroundStyle(.orange) }
        if matches(lower, hints: dimHints) { return Text(raw).foregroundStyle(.secondary) }
        return Text(raw)
    }

    private static func matches(_ line: String, hints: [String]) -> Bool {
        for h in hints {
            if line.contains("level=\(h)") || line.contains("[\(h)]")
                || line.contains(" \(h): ") || line.hasPrefix("\(h):")
                || line.contains("level\":\"\(h)") {
                return true
            }
        }
        return false
    }

    // MARK: - Minimal ANSI SGR parser

    private static let ansiColors: [Int: Color] = [
        30: .primary, 31: .red, 32: .green, 33: .orange, 34: .blue,
        35: .purple, 36: .cyan, 37: .primary,
        90: .secondary, 91: .red, 92: .green, 93: .yellow, 94: .blue,
        95: .purple, 96: .cyan, 97: .primary,
    ]

    /// Parses basic SGR sequences (colors, bold, reset); unknown codes are
    /// dropped. Everything else in the escape family (cursor movement etc.)
    /// is stripped.
    static func ansiText(_ raw: String) -> Text {
        var result = AttributedString()
        var current: Color?
        var bold = false
        var buffer = ""

        func flush() {
            guard !buffer.isEmpty else { return }
            var run = AttributedString(buffer)
            if let current { run.foregroundColor = current }
            if bold { run.inlinePresentationIntent = .stronglyEmphasized }
            result += run
            buffer = ""
        }

        var i = raw.startIndex
        while i < raw.endIndex {
            let ch = raw[i]
            if ch == "\u{1B}", raw.index(after: i) < raw.endIndex, raw[raw.index(after: i)] == "[" {
                // Consume ESC [ params letter
                var j = raw.index(i, offsetBy: 2)
                var params = ""
                while j < raw.endIndex, raw[j].isNumber || raw[j] == ";" {
                    params.append(raw[j])
                    j = raw.index(after: j)
                }
                if j < raw.endIndex {
                    let terminator = raw[j]
                    if terminator == "m" {
                        flush()
                        for code in params.split(separator: ";").compactMap({ Int($0) }) {
                            switch code {
                            case 0: current = nil; bold = false
                            case 1: bold = true
                            case 22: bold = false
                            case 39: current = nil
                            default:
                                if let c = ansiColors[code] { current = c }
                            }
                        }
                        if params.isEmpty { current = nil; bold = false } // ESC[m == reset
                    }
                    i = raw.index(after: j)
                    continue
                }
                break
            }
            buffer.append(ch)
            i = raw.index(after: i)
        }
        flush()
        return Text(result)
    }
}

/// Drop-in line view for streamed log panes.
struct LogLineView: View {
    let line: String

    var body: some View {
        LogStyler.styled(line)
            .font(.system(.caption, design: .monospaced))
            .textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// Colored, auto-scrolling replacement for a TextPane full of log lines.
struct LogPane: View {
    let lines: [String]
    var autoscroll: Bool = true

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 1) {
                    ForEach(Array(lines.enumerated()), id: \.offset) { idx, line in
                        LogLineView(line: line)
                            .id(idx)
                    }
                }
                .padding(8)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .contentSurface()
            .onChange(of: lines.count) { _, newCount in
                guard autoscroll, newCount > 0 else { return }
                proxy.scrollTo(newCount - 1, anchor: .bottom)
            }
        }
    }
}

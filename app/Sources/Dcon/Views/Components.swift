import SwiftUI

/// Circular colored icon avatar for list rows (OrbStack-style): a stable
/// per-item color derived from the seed string, with a white SF Symbol glyph.
struct IconAvatar: View {
    let seed: String
    let symbol: String
    var size: CGFloat = 28
    /// Dim the avatar for stopped/inactive items.
    var active: Bool = true

    private static let palette: [Color] = [
        .blue, .green, .orange, .purple, .pink, .teal, .indigo, .red, .cyan, .mint,
    ]

    private var color: Color {
        // Stable non-cryptographic hash (hashValue is seeded per-launch).
        var h: UInt64 = 5381
        for b in seed.utf8 { h = (h &* 33) &+ UInt64(b) }
        return Self.palette[Int(h % UInt64(Self.palette.count))]
    }

    var body: some View {
        ZStack {
            Circle()
                .fill(active ? color.gradient : Color.gray.opacity(0.45).gradient)
            Image(systemName: symbol)
                .font(.system(size: size * 0.5, weight: .medium))
                .foregroundStyle(.white)
        }
        .frame(width: size, height: size)
    }
}

/// Colored capsule for lifecycle states ("running", "exited", "ready", …).
struct StatusPill: View {
    let text: String

    var body: some View {
        Text(text.capitalized)
            .font(.caption.weight(.medium))
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .background(color.opacity(0.16), in: Capsule())
            .foregroundStyle(color)
    }

    private var color: Color {
        switch text.lowercased() {
        case "running", "ready", "ok": return .green
        case "paused": return .orange
        case "created", "restarting": return .blue
        case "dead": return .red
        default: return .gray
        }
    }
}

/// Small labelled metric tile (stats, dashboard cards).
struct StatTile: View {
    let label: String
    let value: String
    var symbol: String? = nil

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 4) {
                if let symbol {
                    Image(systemName: symbol)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Text(label)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Text(value.isEmpty ? "—" : value)
                .font(.system(.title3, design: .rounded).weight(.semibold))
                .lineLimit(1)
                .minimumScaleFactor(0.6)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(10)
        .cardSurface()
    }
}

/// Toolbar button that copies a string and briefly confirms.
struct CopyButton: View {
    let label: String
    let value: String
    @State private var copied = false

    var body: some View {
        Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(value, forType: .string)
            copied = true
            Task {
                try? await Task.sleep(nanoseconds: 1_200_000_000)
                copied = false
            }
        } label: {
            Label(copied ? "Copied" : label, systemImage: copied ? "checkmark" : "doc.on.doc")
        }
    }
}

// MARK: - Structured JSON inspector

/// One node of a parsed JSON document (dictionaries and arrays become
/// expandable branches; scalars are leaves).
struct JSONNode: Identifiable {
    let id: String
    let key: String
    let scalar: String?
    let children: [JSONNode]?

    static func parse(_ text: String) -> [JSONNode]? {
        guard let data = text.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed]) else {
            return nil
        }
        return nodes(from: obj, path: "$")
    }

    private static func nodes(from value: Any, path: String) -> [JSONNode] {
        if let dict = value as? [String: Any] {
            return dict.keys.sorted().map { key in
                node(key: key, value: dict[key]!, path: "\(path).\(key)")
            }
        }
        if let arr = value as? [Any] {
            return arr.enumerated().map { idx, v in
                node(key: "[\(idx)]", value: v, path: "\(path)[\(idx)]")
            }
        }
        return [JSONNode(id: path, key: "", scalar: scalarString(value), children: nil)]
    }

    private static func node(key: String, value: Any, path: String) -> JSONNode {
        if value is [String: Any] || value is [Any] {
            let kids = nodes(from: value, path: path)
            let summary = (value as? [Any]).map { "\($0.count) items" } ?? "\(kids.count) fields"
            return JSONNode(id: path, key: key, scalar: kids.isEmpty ? "empty" : summary, children: kids.isEmpty ? nil : kids)
        }
        return JSONNode(id: path, key: key, scalar: scalarString(value), children: nil)
    }

    private static func scalarString(_ value: Any) -> String {
        if value is NSNull { return "null" }
        if let b = value as? Bool { return b ? "true" : "false" }
        return "\(value)"
    }
}

/// Expandable key/value tree for inspect output, with a raw-JSON fallback
/// toggle. Feels like Xcode's property inspector rather than a text dump.
struct JSONInspectorView: View {
    let jsonText: String
    @State private var showRaw = false

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Picker("", selection: $showRaw) {
                    Text("Structured").tag(false)
                    Text("Raw").tag(true)
                }
                .pickerStyle(.segmented)
                .frame(width: 180)
                .labelsHidden()
                Spacer()
                CopyButton(label: "Copy", value: jsonText)
                    .controlSize(.small)
            }
            .padding(8)
            .chromeStyle()
            Divider()
            if showRaw {
                TextPane(text: jsonText)
            } else if let nodes = JSONNode.parse(jsonText) {
                List(nodes, children: \.children) { node in
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        if !node.key.isEmpty {
                            Text(node.key)
                                .font(.system(.callout, design: .monospaced).weight(.medium))
                        }
                        if let scalar = node.scalar {
                            Text(scalar)
                                .font(.system(.callout, design: .monospaced))
                                .foregroundStyle(node.children == nil ? Color.primary.opacity(0.75) : Color.secondary)
                                .textSelection(.enabled)
                                .lineLimit(3)
                        }
                        Spacer(minLength: 0)
                    }
                }
                .listStyle(.inset)
            } else {
                TextPane(text: jsonText)
            }
        }
    }
}

/// Sheet wrapper: runs a dcon command and shows the result in the structured
/// inspector (or plain text if the output isn't JSON).
struct InspectSheet: View {
    let title: String
    let args: [String]
    @Environment(\.dismiss) private var dismiss
    @State private var output = ""
    @State private var loaded = false

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text(title).font(.headline)
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            }
            .padding(12)
            .chromeStyle()
            Divider()
            if loaded {
                JSONInspectorView(jsonText: output)
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .paneStyle()
        .frame(minWidth: 640, minHeight: 480)
        .onExitCommand { dismiss() }
        .task {
            do {
                output = try await DconCLI.shared.capture(args)
            } catch {
                output = error.localizedDescription
            }
            loaded = true
        }
    }
}

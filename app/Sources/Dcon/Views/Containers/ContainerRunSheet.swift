import SwiftUI

/// Form sheet to launch a new container (`dcon run`). Flags mirror
/// cmd/run.go's `addRunFlags`/`buildContainerArgs`: -d, --rm, -p, -e, -v, -w,
/// -u, --name, -it.
struct ContainerRunSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss

    @State private var image = ""
    @State private var name = ""
    @State private var command = ""
    @State private var ports: [String] = [""]
    @State private var envVars: [String] = [""]
    @State private var volumes: [String] = [""]
    @State private var workdir = ""
    @State private var user = ""
    @State private var detach = true
    @State private var removeOnExit = false
    @State private var interactiveTTY = false

    /// Image references from `state.images` that match the current text,
    /// deduped and capped so the popover list stays short.
    private var imageSuggestions: [String] {
        let query = image.trimmingCharacters(in: .whitespaces).lowercased()
        guard !query.isEmpty else { return [] }
        let refs = Set(state.images.map(\.reference))
        let matches = refs.filter { $0.lowercased().contains(query) && $0.lowercased() != query }
        return Array(matches.sorted().prefix(8))
    }

    private var canLaunch: Bool { !image.trimmingCharacters(in: .whitespaces).isEmpty }

    var body: some View {
        VStack(spacing: 0) {
            Text("New Container")
                .font(.headline)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
                .chromeStyle()
            Divider()
            Form {
                Section("Image") {
                    TextField("nginx:latest", text: $image)
                    ForEach(imageSuggestions, id: \.self) { suggestion in
                        Button(suggestion) { image = suggestion }
                            .buttonStyle(.plain)
                            .foregroundStyle(.secondary)
                            .font(.callout)
                    }
                }
                Section("Identity") {
                    TextField("Name (optional)", text: $name)
                }
                Section("Execution") {
                    TextField("Command override (optional)", text: $command)
                    TextField("Working directory", text: $workdir)
                    TextField("User", text: $user)
                    Toggle("Detached (-d)", isOn: $detach)
                    Toggle("Remove on exit (--rm)", isOn: $removeOnExit)
                    Toggle("Interactive TTY (opens in Terminal)", isOn: $interactiveTTY)
                }
                Section("Ports (host:container)") {
                    EditableStringRows(rows: $ports, placeholder: "8080:80")
                }
                Section("Environment (KEY=VALUE)") {
                    EditableStringRows(rows: $envVars, placeholder: "KEY=value")
                }
                Section("Volumes (host:container)") {
                    EditableStringRows(rows: $volumes, placeholder: "/host/path:/container/path")
                }
            }
            .formStyle(.grouped)
            Divider()
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Run") { launch() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!canLaunch)
            }
            .padding(12)
            .chromeStyle()
        }
        .frame(width: 560, height: 680)
    }

    /// Builds `["run", ...flags..., IMAGE, ...command]`. Flags must precede
    /// IMAGE — `addRunFlags` sets `SetInterspersed(false)`, so everything
    /// after IMAGE is treated as the container's command.
    private func buildArgs() -> [String] {
        var args = ["run"]
        if removeOnExit { args.append("--rm") }
        if interactiveTTY {
            args.append("-it")
        } else if detach {
            args.append("-d")
        }
        let trimmedName = name.trimmingCharacters(in: .whitespaces)
        if !trimmedName.isEmpty { args += ["--name", trimmedName] }
        for p in ports.map({ $0.trimmingCharacters(in: .whitespaces) }) where !p.isEmpty {
            args += ["-p", p]
        }
        for e in envVars.map({ $0.trimmingCharacters(in: .whitespaces) }) where !e.isEmpty {
            args += ["-e", e]
        }
        for v in volumes.map({ $0.trimmingCharacters(in: .whitespaces) }) where !v.isEmpty {
            args += ["-v", v]
        }
        let trimmedWorkdir = workdir.trimmingCharacters(in: .whitespaces)
        if !trimmedWorkdir.isEmpty { args += ["-w", trimmedWorkdir] }
        let trimmedUser = user.trimmingCharacters(in: .whitespaces)
        if !trimmedUser.isEmpty { args += ["-u", trimmedUser] }
        args.append(image.trimmingCharacters(in: .whitespaces))
        args.append(contentsOf: command.split(whereSeparator: \.isWhitespace).map(String.init))
        return args
    }

    private func launch() {
        let args = buildArgs()
        if interactiveTTY {
            TerminalLauncher.run(dconArgs: args)
        } else {
            state.performDetached(args)
        }
        dismiss()
    }
}

/// Add/remove list of freeform text rows, used for ports/env/volumes.
private struct EditableStringRows: View {
    @Binding var rows: [String]
    let placeholder: String

    var body: some View {
        ForEach(rows.indices, id: \.self) { idx in
            HStack {
                TextField(placeholder, text: $rows[idx])
                Button {
                    rows.remove(at: idx)
                    if rows.isEmpty { rows = [""] }
                } label: {
                    Image(systemName: "minus.circle")
                }
                .buttonStyle(.plain)
            }
        }
        Button {
            rows.append("")
        } label: {
            Label("Add", systemImage: "plus.circle")
        }
        .buttonStyle(.plain)
    }
}

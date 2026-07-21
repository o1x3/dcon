import SwiftUI

/// OrbStack-style persistent Linux machines: create, shell into, start/stop,
/// set the default, and remove long-lived microVMs.
struct MachinesView: View {
    @EnvironmentObject var state: AppState

    @State private var showCreateSheet = false
    @State private var outputSheet: OutputSheetRequest?
    @State private var removeTarget: MachineRow?
    @State private var showRemoveConfirm = false

    var body: some View {
        Group {
            if state.machines.isEmpty {
                EmptyListView(
                    title: "No Machines",
                    symbol: "desktopcomputer",
                    description: "Create a persistent Linux machine to open a shell into."
                )
            } else {
                Table(state.machines) {
                    TableColumn("") { row in
                        if row.Default {
                            Image(systemName: "star.fill")
                                .foregroundStyle(.yellow)
                                .help("Default machine")
                        }
                    }
                    .width(20)
                    TableColumn("Name", value: \.Name)
                    TableColumn("Distro", value: \.Distro)
                    TableColumn("State") { row in
                        HStack(spacing: 6) {
                            Circle().fill(stateColor(row.State)).frame(width: 8, height: 8)
                            Text(row.State)
                        }
                    }
                    TableColumn("CPUs", value: \.CPUs)
                    TableColumn("Memory", value: \.Memory)
                    TableColumn("Created", value: \.Created)
                    TableColumn("") { row in
                        actionsMenu(for: row)
                    }
                    .width(32)
                }
            }
        }
        .navigationTitle("Machines")
        .toolbar {
            ToolbarItemGroup {
                RefreshButton()
                Button {
                    showCreateSheet = true
                } label: {
                    Label("New Machine…", systemImage: "plus")
                }
            }
        }
        .sheet(isPresented: $showCreateSheet) {
            NewMachineSheet()
        }
        .sheet(item: $outputSheet) { req in
            CommandOutputSheet(title: req.title, args: req.args)
        }
        .confirmDialog(
            "Delete machine \(removeTarget?.Name ?? "")? Its filesystem is permanently lost.",
            isPresented: $showRemoveConfirm
        ) {
            if let name = removeTarget?.Name {
                Task { await state.perform(["machine", "rm", name]) }
            }
        }
    }

    private func stateColor(_ s: String) -> Color {
        switch s.lowercased() {
        case "running": return .green
        case "stopping": return .orange
        case "stopped": return .secondary
        default: return .gray
        }
    }

    @ViewBuilder
    private func actionsMenu(for row: MachineRow) -> some View {
        Menu {
            Button("Open Shell") {
                TerminalLauncher.run(dconArgs: ["machine", "shell", row.Name])
            }
            if row.isRunning {
                Button("Stop") { Task { await state.perform(["machine", "stop", row.Name]) } }
            } else {
                Button("Start") { Task { await state.perform(["machine", "start", row.Name]) } }
            }
            Button("Make Default") {
                Task { await state.perform(["machine", "default", row.Name]) }
            }
            .disabled(row.Default)
            Button("Info") {
                outputSheet = OutputSheetRequest(title: "Machine: \(row.Name)", args: ["machine", "info", row.Name])
            }
            Divider()
            Button("Remove…", role: .destructive) {
                removeTarget = row
                showRemoveConfirm = true
            }
        } label: {
            Image(systemName: "ellipsis.circle")
        }
        .menuStyle(.borderlessButton)
        .frame(width: 24)
    }
}

/// Sheet for `machine create DISTRO [NAME]` with the resource flags the
/// backend actually honors (`--cpus`, `-m/--memory`, `--mount-home`).
private struct NewMachineSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss

    static let distros = [
        "alma", "alpine", "arch", "centos", "debian", "devuan", "fedora", "gentoo",
        "kali", "nixos", "openeuler", "opensuse", "oracle", "rocky", "ubuntu", "void",
    ]

    @State private var distro = "ubuntu"
    @State private var name = ""
    @State private var cpus = ""
    @State private var memory = ""
    @State private var mountHome = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("New Machine").font(.headline)

            Form {
                Picker("Distro", selection: $distro) {
                    ForEach(Self.distros, id: \.self) { Text($0).tag($0) }
                }
                TextField("Name (optional)", text: $name)
                TextField("CPUs (optional)", text: $cpus)
                TextField("Memory, e.g. 4G (optional)", text: $memory)
                Toggle("Mount macOS home at /mnt/mac", isOn: $mountHome)
            }

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Create") {
                    state.performDetached(createArgs)
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 380)
    }

    private var createArgs: [String] {
        var args = ["machine", "create", distro]
        let trimmedName = name.trimmingCharacters(in: .whitespaces)
        if !trimmedName.isEmpty { args.append(trimmedName) }
        let trimmedCPUs = cpus.trimmingCharacters(in: .whitespaces)
        if !trimmedCPUs.isEmpty { args.append(contentsOf: ["--cpus", trimmedCPUs]) }
        let trimmedMemory = memory.trimmingCharacters(in: .whitespaces)
        if !trimmedMemory.isEmpty { args.append(contentsOf: ["--memory", trimmedMemory]) }
        if mountHome { args.append("--mount-home") }
        return args
    }
}

import SwiftUI

/// OrbStack-style persistent Linux machines: create, shell into, start/stop,
/// set the default, and remove long-lived microVMs.
struct MachinesView: View {
    @EnvironmentObject var state: AppState

    @State private var showCreateSheet = false
    @State private var outputSheet: OutputSheetRequest?
    @State private var removeTarget: MachineRow?
    @State private var showRemoveConfirm = false
    @State private var selection = Set<MachineRow.ID>()
    @State private var sortOrder: [KeyPathComparator<MachineRow>] = [KeyPathComparator(\.Name)]

    private var sortedMachines: [MachineRow] {
        state.machines.sorted(using: sortOrder)
    }

    var body: some View {
        Group {
            if state.machines.isEmpty {
                ContentUnavailableView {
                    Label("No Machines", systemImage: "desktopcomputer")
                } description: {
                    Text("Create a persistent Linux machine to open a shell into.")
                } actions: {
                    Button {
                        showCreateSheet = true
                    } label: {
                        Label("New Machine…", systemImage: "plus")
                    }
                    .buttonStyle(.borderedProminent)
                }
            } else {
                Table(sortedMachines, selection: $selection, sortOrder: $sortOrder) {
                    TableColumn("") { row in
                        if row.Default {
                            Image(systemName: "star.fill")
                                .foregroundStyle(Color.accentColor)
                                .help("Default machine")
                        }
                    }
                    .width(20)
                    TableColumn("Name", value: \.Name)
                    TableColumn("Distro", value: \.Distro)
                    TableColumn("State", sortUsing: KeyPathComparator(\.State)) { row in
                        StatusPill(text: row.State)
                    }
                    TableColumn("CPUs", value: \.CPUs)
                    TableColumn("Memory", value: \.Memory)
                    TableColumn("Created", value: \.Created)
                    TableColumn("") { row in
                        HStack(spacing: 6) {
                            Button {
                                TerminalLauncher.run(dconArgs: ["machine", "shell", row.Name])
                            } label: {
                                Label("Open Shell", systemImage: "terminal")
                            }
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                            actionsMenu(for: row)
                        }
                    }
                    .width(150)
                }
                .contextMenu(forSelectionType: MachineRow.ID.self) { ids in
                    contextMenuItems(for: ids)
                } primaryAction: { ids in
                    if let row = sortedMachines.first(where: { ids.contains($0.id) }) {
                        TerminalLauncher.run(dconArgs: ["machine", "shell", row.Name])
                    }
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

    /// Shared action set (used by both the per-row ellipsis menu and the
    /// table's right-click context menu).
    @ViewBuilder
    private func rowActions(for row: MachineRow) -> some View {
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
    }

    @ViewBuilder
    private func actionsMenu(for row: MachineRow) -> some View {
        Menu {
            rowActions(for: row)
        } label: {
            Image(systemName: "ellipsis.circle")
        }
        .menuStyle(.borderlessButton)
        .frame(width: 24)
    }

    @ViewBuilder
    private func contextMenuItems(for ids: Set<MachineRow.ID>) -> some View {
        let rows = sortedMachines.filter { ids.contains($0.id) }
        if rows.count == 1, let row = rows.first {
            Button("Open Shell") {
                TerminalLauncher.run(dconArgs: ["machine", "shell", row.Name])
            }
            Divider()
            rowActions(for: row)
        }
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
                Section {
                    Picker("Distro", selection: $distro) {
                        ForEach(Self.distros, id: \.self) { d in
                            Text(d.capitalized).tag(d)
                        }
                    }
                    TextField("Name (optional)", text: $name)
                }
                Section {
                    HStack {
                        TextField("CPUs (optional)", text: $cpus)
                        TextField("Memory, e.g. 4G (optional)", text: $memory)
                    }
                }
                Section {
                    Toggle("Mount macOS home at /mnt/mac", isOn: $mountHome)
                } footer: {
                    Text("Makes your Mac home directory available inside the machine at /mnt/mac.")
                }
            }
            .formStyle(.grouped)

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Create") {
                    state.performDetached(createArgs)
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(distro.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 420, height: 420)
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

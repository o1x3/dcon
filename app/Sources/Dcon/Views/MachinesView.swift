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
                    .width(min: 20, ideal: 20, max: 20)
                    TableColumn("Name", value: \.Name) { row in
                        HStack(spacing: 8) {
                            IconAvatar(seed: row.Distro, symbol: "desktopcomputer", size: 24, active: row.isRunning)
                            Text(row.Name)
                                .font(.system(.body, design: .monospaced))
                                .lineLimit(1)
                                .truncationMode(.middle)
                        }
                        .padding(.vertical, 2)
                    }
                    .width(min: 100, ideal: 160)
                    TableColumn("Distro", value: \.Distro)
                        .width(min: 80, ideal: 100)
                    TableColumn("State", sortUsing: KeyPathComparator(\.State)) { row in
                        StatusPill(text: row.State)
                    }
                    .width(min: 70, ideal: 90)
                    TableColumn("CPUs", value: \.CPUs)
                        .width(min: 50, ideal: 60)
                    TableColumn("Memory", value: \.Memory)
                        .width(min: 60, ideal: 80)
                    TableColumn("Created", value: \.Created)
                        .width(min: 90, ideal: 130)
                    TableColumn("") { row in
                        HStack(spacing: 6) {
                            Button {
                                TerminalLauncher.run(dconArgs: ["machine", "shell", row.Name])
                            } label: {
                                Label("Open Shell", systemImage: "terminal")
                            }
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                            .help("Open a shell in \(row.Name)")
                            actionsMenu(for: row)
                        }
                    }
                    .width(min: 150, ideal: 150)
                }
                .animation(.default, value: sortedMachines)
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
                Button {
                    showCreateSheet = true
                } label: {
                    Label("New Machine…", systemImage: "plus")
                }
                .help("Create a new machine")
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
            Label("More Actions", systemImage: "ellipsis.circle")
                .labelStyle(.iconOnly)
        }
        .menuStyle(.borderlessButton)
        .frame(width: 24)
        .help("More actions for \(row.Name)")
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
    @FocusState private var nameFieldFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            Text("New Machine")
                .font(.headline)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
                .chromeStyle()
            Divider()

            Form {
                Section {
                    Picker("Distro", selection: $distro) {
                        ForEach(Self.distros, id: \.self) { d in
                            Text(d.capitalized).tag(d)
                        }
                    }
                    TextField("Name (optional)", text: $name)
                        .focused($nameFieldFocused)
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

            Divider()
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Create") {
                    state.performDetached(createArgs)
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(distro.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding(12)
            .chromeStyle()
        }
        .frame(minWidth: 420, idealWidth: 420, minHeight: 420, idealHeight: 420)
        .task { nameFieldFocused = true }
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

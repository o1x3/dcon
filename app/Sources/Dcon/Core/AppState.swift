import Foundation
import SwiftUI

/// Sidebar sections of the main window.
enum SidebarSection: String, CaseIterable, Identifiable {
    case containers = "Containers"
    case images = "Images"
    case volumes = "Volumes"
    case networks = "Networks"
    case machines = "Machines"
    case warmPool = "Warm Pool"
    case compose = "Compose"
    case system = "System"

    var id: String { rawValue }

    var symbol: String {
        switch self {
        case .containers: return "shippingbox"
        case .images: return "opticaldiscdrive"
        case .volumes: return "externaldrive"
        case .networks: return "network"
        case .machines: return "desktopcomputer"
        case .warmPool: return "flame"
        case .compose: return "square.stack.3d.up"
        case .system: return "gearshape.2"
        }
    }
}

/// Central observable store. Polls the CLI for state and exposes actions.
/// All published state is main-actor.
@MainActor
final class AppState: ObservableObject {
    let cli = DconCLI.shared

    // Navigation
    @Published var section: SidebarSection = .containers

    // Data
    @Published var containers: [ContainerRow] = []
    @Published var images: [ImageRow] = []
    @Published var volumes: [VolumeRow] = []
    @Published var networks: [NetworkRow] = []
    @Published var machines: [MachineRow] = []
    @Published var warmMembers: [WarmRow] = []
    @Published var stats: [StatsRow] = []
    @Published var systemStatus: SystemStatus = .unknown("not checked")
    @Published var runtimeAvailable: Bool = true
    @Published var cliAvailable: Bool = true

    // UX
    @Published var lastError: String?
    @Published var busy: Bool = false

    private var pollTask: Task<Void, Never>?

    var runningContainers: [ContainerRow] { containers.filter(\.isRunning) }

    // MARK: - Polling

    func startPolling() {
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refreshAll()
                // Interval is re-read each cycle so Settings changes apply live.
                try? await Task.sleep(nanoseconds: UInt64(AppSettings.pollInterval * 1_000_000_000))
            }
        }
    }

    func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    /// Refresh backend status plus the data behind every section. Failures are
    /// silent (the backend may simply be stopped); explicit actions surface
    /// their own errors.
    func refreshAll() async {
        cliAvailable = cli.binaryURL != nil
        guard cliAvailable else { return }

        await refreshRuntimeAvailable()
        await refreshSystemStatus()
        guard runtimeAvailable, systemStatus.isRunning else {
            containers = []
            stats = []
            machines = []
            warmMembers = []
            return
        }

        async let c = try? cli.jsonLines(ContainerRow.self, ["ps", "-a", "--format", "json"])
        async let i = try? cli.jsonLines(ImageRow.self, ["images", "--format", "json"])
        async let v = try? cli.jsonLines(VolumeRow.self, ["volume", "ls", "--format", "json"])
        async let n = try? cli.jsonLines(NetworkRow.self, ["network", "ls", "--format", "json"])
        async let m = try? cli.jsonLines(MachineRow.self, ["machine", "ls", "--format", "json"])
        async let w = try? cli.capture(["warm", "ls"])

        containers = await c ?? []
        images = await i ?? []
        volumes = await v ?? []
        networks = await n ?? []
        machines = await m ?? []
        warmMembers = Self.parseWarmLs(await w ?? "")
    }

    func refreshSystemStatus() async {
        guard runtimeAvailable else {
            systemStatus = .unknown("runtime not installed")
            return
        }
        guard let out = try? await cli.capture(["system", "status"]) else {
            systemStatus = .unknown("status unavailable")
            return
        }
        var kv: [String: String] = [:]
        for line in out.split(separator: "\n") {
            let fields = line.split(separator: " ", omittingEmptySubsequences: true)
            guard fields.count >= 2 else { continue }
            kv[fields[0].lowercased()] = fields.dropFirst().joined(separator: " ")
        }
        switch kv["status"]?.lowercased() {
        case "running": systemStatus = .running
        case .some(let s): systemStatus = s.contains("stop") ? .stopped : .unknown(s)
        case .none: systemStatus = out.lowercased().contains("running") ? .running : .stopped
        }
    }

    /// Probe whether Apple's `container` runtime is installed (`dcon version`).
    func refreshRuntimeAvailable() async {
        struct VersionInfo: Decodable {
            struct Component: Decodable { let Version: String }
            let Server: Component
        }
        guard let out = try? await cli.capture(["version", "--format", "json"]),
              let data = out.data(using: .utf8),
              let info = try? JSONDecoder().decode(VersionInfo.self, from: data) else {
            runtimeAvailable = false
            return
        }
        runtimeAvailable = info.Server.Version != "unknown"
    }

    /// Fetch live stats once (`stats --no-stream`); used by the containers view.
    func refreshStats() async {
        stats = (try? await cli.jsonLines(StatsRow.self, ["stats", "--no-stream", "--format", "json"])) ?? []
    }

    /// Parse `dcon warm ls` tabular output (CONTAINER ID / IMAGE / AGE / STATE).
    static func parseWarmLs(_ out: String) -> [WarmRow] {
        var rows: [WarmRow] = []
        for (idx, line) in out.split(separator: "\n").enumerated() {
            if idx == 0 { continue } // header
            if line.hasPrefix("(pool empty)") { continue }
            let fields = line.split(separator: " ", omittingEmptySubsequences: true).map(String.init)
            guard fields.count >= 4 else { continue }
            rows.append(WarmRow(containerID: fields[0], image: fields[1], age: fields[2], state: fields[3]))
        }
        return rows
    }

    // MARK: - Actions

    /// Run a dcon command; on failure surface the error, then refresh.
    func perform(_ args: [String], cwd: URL? = nil) async {
        busy = true
        defer { busy = false }
        do {
            _ = try await cli.capture(args, cwd: cwd)
        } catch {
            lastError = error.localizedDescription
        }
        await refreshAll()
    }

    /// Run a dcon command in the background without blocking the UI busy state
    /// (pulls, system start — long-running but safe to fire and refresh later).
    func performDetached(_ args: [String]) {
        Task {
            do {
                _ = try await cli.capture(args)
            } catch {
                lastError = error.localizedDescription
            }
            await refreshAll()
        }
    }
}

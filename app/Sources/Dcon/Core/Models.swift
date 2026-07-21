import Foundation

// Codable mirrors of the JSON emitted by `dcon <cmd> --format json`
// (one object per line, docker's convention). Field names match the CLI's
// view structs exactly.

struct ContainerRow: Codable, Identifiable, Hashable {
    let ID: String
    let Image: String
    let Command: String
    let CreatedAt: String
    let RunningFor: String
    let Status: String
    let Ports: String
    let Names: String
    let Labels: String
    let Mounts: String
    let Networks: String
    let State: String

    var id: String { self.ID }
    var isRunning: Bool { State.lowercased() == "running" }
    var isPaused: Bool { State.lowercased() == "paused" }
    var shortID: String { String(self.ID.prefix(12)) }
}

struct ImageRow: Codable, Identifiable, Hashable {
    let Repository: String
    let Tag: String
    let ID: String
    let Digest: String
    let CreatedSince: String
    let CreatedAt: String
    let Size: String

    var id: String { "\(Repository):\(Tag)@\(self.ID)" }
    var reference: String {
        Tag.isEmpty || Tag == "<none>" ? Repository : "\(Repository):\(Tag)"
    }
}

struct VolumeRow: Codable, Identifiable, Hashable {
    let Name: String
    let Driver: String
    let Scope: String
    let Mountpoint: String
    let Labels: String

    var id: String { Name }
}

struct NetworkRow: Codable, Identifiable, Hashable {
    let ID: String
    let Name: String
    let Driver: String
    let Scope: String
    let Subnet: String

    var id: String { self.ID }
}

struct StatsRow: Codable, Identifiable, Hashable {
    let Container: String
    let Name: String
    let ID: String
    let CPUPerc: String
    let MemUsage: String
    let MemPerc: String
    let NetIO: String
    let BlockIO: String
    let PIDs: String

    var id: String { self.ID }
}

struct MachineRow: Codable, Identifiable, Hashable {
    let Name: String
    let Distro: String
    let State: String
    let CPUs: String
    let Memory: String
    let Created: String
    let Default: Bool

    var id: String { Name }
    var isRunning: Bool { State.lowercased() == "running" }
}

struct HistoryRow: Codable, Identifiable, Hashable {
    let ID: String
    let CreatedSince: String
    let CreatedBy: String
    let Size: String
    let Comment: String

    var id: String { "\(self.ID)-\(CreatedBy.hashValue)" }
}

struct DFRow: Codable, Identifiable, Hashable {
    let TypeName: String
    let TotalCount: String
    let Active: String
    let Size: String
    let Reclaimable: String

    var id: String { TypeName }

    enum CodingKeys: String, CodingKey {
        case TypeName = "Type"
        case TotalCount, Active, Size, Reclaimable
    }
}

/// One member of the warm pool, parsed from `dcon warm ls` tabular output.
struct WarmRow: Identifiable, Hashable {
    let containerID: String
    let image: String
    let age: String
    let state: String

    var id: String { containerID }
}

/// Backend runtime status, parsed from `dcon system status`.
enum SystemStatus: Equatable {
    case running
    case stopped
    case unknown(String)

    var label: String {
        switch self {
        case .running: return "Running"
        case .stopped: return "Stopped"
        case .unknown: return "Unknown"
        }
    }

    var isRunning: Bool { self == .running }
}

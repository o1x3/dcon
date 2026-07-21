import Foundation
import SwiftUI

/// User-tunable app preferences, backed by UserDefaults.
enum AppSettings {
    static let pollIntervalKey = "pollInterval"
    static let dconPathKey = "dconPath"
    static let appearanceKey = "appearance"
    static let startBackendOnLaunchKey = "startBackendOnLaunch"

    /// Poll interval in seconds (clamped 1–30, default 3).
    static var pollInterval: Double {
        let v = UserDefaults.standard.double(forKey: pollIntervalKey)
        return v == 0 ? 3 : min(max(v, 1), 30)
    }

    /// Explicit dcon binary override; empty = automatic discovery.
    static var dconPath: String {
        UserDefaults.standard.string(forKey: dconPathKey) ?? ""
    }
}

/// Appearance override (system / light / dark).
enum AppearanceChoice: String, CaseIterable, Identifiable {
    case system, light, dark

    var id: String { rawValue }

    var label: String {
        switch self {
        case .system: return "System"
        case .light: return "Light"
        case .dark: return "Dark"
        }
    }

    var nsAppearance: NSAppearance? {
        switch self {
        case .system: return nil
        case .light: return NSAppearance(named: .aqua)
        case .dark: return NSAppearance(named: .darkAqua)
        }
    }

    func apply() {
        NSApp.appearance = nsAppearance
    }
}

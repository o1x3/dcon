import Foundation

/// Identifiable wrapper so `.sheet(item:)` can drive a `CommandOutputSheet`
/// (inspect, history, ...) for any dcon command from the Images, Volumes, and
/// Networks views.
struct OutputRequest: Identifiable {
    let id = UUID()
    let title: String
    let args: [String]
}

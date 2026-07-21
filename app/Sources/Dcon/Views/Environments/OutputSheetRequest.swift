import Foundation

/// Identifiable wrapper so `.sheet(item:)` can present a `CommandOutputSheet`
/// for an arbitrary read-only dcon command from any Environments view.
struct OutputSheetRequest: Identifiable {
    let id = UUID()
    let title: String
    let args: [String]
}

import SwiftUI

/// Empty-list placeholder with a primary call-to-action button, for sections
/// where the obvious next step is a single action (pull an image, create a
/// volume, create a network). Mirrors `EmptyListView` but adds the action —
/// kept separate so the plain (no-CTA) empty states elsewhere are untouched.
struct EmptyStateView: View {
    let title: String
    let symbol: String
    var description: String = ""
    let actionTitle: String
    let action: () -> Void

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: symbol)
        } description: {
            Text(description)
        } actions: {
            Button(action: action) {
                Text(actionTitle)
                    .frame(minWidth: 120)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
        }
    }
}

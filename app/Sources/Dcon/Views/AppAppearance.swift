import SwiftUI

/// Shared pane backgrounds, materials, and surface styles for a uniform window look.
enum AppAppearance {
    /// Toolbar/footer chrome — matches the unified title bar.
    static let chrome = Material.bar

    /// Inset scrollable content (logs, inspect, monospace output).
    static let content = Material.thin

    /// Elevated card/tile surfaces.
    static let card = Material.ultraThin
}

extension View {
    /// Full-pane background matching NavigationSplitView content columns.
    func paneStyle() -> some View {
        frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(.background)
    }

    /// Chrome strip (toolbars, footers) with bar material.
    func chromeStyle() -> some View {
        background(AppAppearance.chrome)
    }

    /// Blurred content surface for monospace/scroll areas.
    func contentSurface() -> some View {
        background(AppAppearance.content)
    }

    /// Subtle elevated card with rounded corners.
    func cardSurface(cornerRadius: CGFloat = 8) -> some View {
        background(AppAppearance.card, in: RoundedRectangle(cornerRadius: cornerRadius))
    }
}

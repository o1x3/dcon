import SwiftUI

@main
struct DconApp: App {
    @StateObject private var state = AppState()

    var body: some Scene {
        WindowGroup("Dcon", id: "main") {
            MainWindow()
                .environmentObject(state)
                .frame(minWidth: 900, minHeight: 560)
                .task { state.startPolling() }
        }
        .windowToolbarStyle(.unified)

        MenuBarExtra {
            MenuBarView()
                .environmentObject(state)
        } label: {
            Image(systemName: state.systemStatus.isRunning ? "shippingbox.fill" : "shippingbox")
        }
    }
}

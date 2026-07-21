import SwiftUI

// Temporary stand-ins for section views while they are being built out.
// Each gets replaced by a dedicated file.

struct ContainersView: View {
    var body: some View { EmptyListView(title: "Containers", symbol: "shippingbox") }
}

struct ImagesView: View {
    var body: some View { EmptyListView(title: "Images", symbol: "opticaldiscdrive") }
}

struct VolumesView: View {
    var body: some View { EmptyListView(title: "Volumes", symbol: "externaldrive") }
}

struct NetworksView: View {
    var body: some View { EmptyListView(title: "Networks", symbol: "network") }
}

struct MachinesView: View {
    var body: some View { EmptyListView(title: "Machines", symbol: "desktopcomputer") }
}

struct WarmPoolView: View {
    var body: some View { EmptyListView(title: "Warm Pool", symbol: "flame") }
}

struct ComposeView: View {
    var body: some View { EmptyListView(title: "Compose", symbol: "square.stack.3d.up") }
}

struct SystemView: View {
    var body: some View { EmptyListView(title: "System", symbol: "gearshape.2") }
}

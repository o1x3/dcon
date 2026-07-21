import SwiftUI

/// Port mappings parsed from the row's `Ports` field (docker-style
/// "0.0.0.0:8080->80/tcp, ..."), plus a lookup sheet backed by `dcon port`.
struct ContainerPortsPane: View {
    let container: ContainerRow

    @State private var showPortLookup = false

    private var mappings: [String] {
        container.Ports
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Port Mappings").font(.headline)
                Spacer()
                Button("Port…") { showPortLookup = true }
                    .help("Look up published ports (dcon port)")
            }
            .padding(8)
            .chromeStyle()
            Divider()
            if mappings.isEmpty {
                EmptyListView(title: "No Published Ports", symbol: "network", description: "This container has no published ports.")
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(mappings, id: \.self) { mapping in
                            Text(mapping)
                                .font(.system(.body, design: .monospaced))
                                .lineLimit(1)
                                .truncationMode(.middle)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.horizontal, 12)
                                .padding(.vertical, 6)
                            Divider()
                        }
                    }
                    .animation(.default, value: mappings)
                }
                .contentSurface()
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .sheet(isPresented: $showPortLookup) {
            CommandOutputSheet(title: "dcon port \(container.shortID)", args: ["port", container.id])
        }
    }
}

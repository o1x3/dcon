import SwiftUI

/// Live resource usage for the selected container, sourced from
/// `state.stats` (populated by `dcon stats --no-stream --format json`).
/// Polls `state.refreshStats()` every 2s while visible.
///
/// Note: `dcon stats`' Name/Container/ID fields are all the (possibly
/// truncated) container ID, not the human name (see cmd/stats.go
/// `renderStats`) — matching is done by ID prefix in both directions rather
/// than against `container.Names`.
struct ContainerStatsPane: View {
    let container: ContainerRow
    @EnvironmentObject var state: AppState

    @State private var pollTask: Task<Void, Never>?

    private var stats: StatsRow? {
        state.stats.first {
            container.id.hasPrefix($0.ID) || $0.ID.hasPrefix(container.shortID)
        }
    }

    var body: some View {
        Group {
            if let stats {
                ScrollView {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 160), spacing: 12)], spacing: 12) {
                        StatTile(title: "CPU", value: stats.CPUPerc)
                        StatTile(title: "Memory", value: "\(stats.MemUsage)  (\(stats.MemPerc))")
                        StatTile(title: "Net I/O", value: stats.NetIO)
                        StatTile(title: "Block I/O", value: stats.BlockIO)
                        StatTile(title: "PIDs", value: stats.PIDs)
                    }
                    .padding(12)
                }
            } else {
                EmptyListView(
                    title: "No Stats",
                    symbol: "chart.bar",
                    description: container.isRunning ? "Waiting for stats…" : "Container is not running."
                )
            }
        }
        .onAppear { startPolling() }
        .onDisappear { stopPolling() }
    }

    private func startPolling() {
        pollTask?.cancel()
        pollTask = Task {
            while !Task.isCancelled {
                await state.refreshStats()
                try? await Task.sleep(nanoseconds: 2_000_000_000)
            }
        }
    }

    private func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }
}

private struct StatTile: View {
    let title: String
    let value: String

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title.uppercased())
                .font(.caption)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.title3)
                .monospacedDigit()
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 8))
    }
}

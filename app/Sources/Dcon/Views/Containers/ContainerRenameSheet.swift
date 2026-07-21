import SwiftUI

/// Sheet to rename a container.
///
/// Note: the dcon backend does not support renaming — `dcon rename` always
/// hard-errors with "rename is not supported: a container's name is its
/// immutable ID in the backend" (cmd/lifecycle.go `newRenameCmd`). This sheet
/// is kept for Docker UI parity; submitting surfaces that error through the
/// standard `state.lastError` alert, same as every other action.
struct ContainerRenameSheet: View {
    let container: ContainerRow
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var newName: String = ""
    @FocusState private var nameFieldFocused: Bool

    private var trimmedName: String { newName.trimmingCharacters(in: .whitespaces) }

    var body: some View {
        VStack(spacing: 0) {
            Text("Rename Container")
                .font(.headline)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
                .chromeStyle()
            Divider()
            VStack(alignment: .leading, spacing: 12) {
                Text(container.Names)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                TextField("New name", text: $newName)
                    .textFieldStyle(.roundedBorder)
                    .focused($nameFieldFocused)
                    .onSubmit(rename)
            }
            .padding(16)
            Divider()
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Rename") { rename() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(trimmedName.isEmpty)
            }
            .padding(12)
            .chromeStyle()
        }
        .frame(width: 360)
        .onAppear {
            newName = container.Names
            nameFieldFocused = true
        }
    }

    private func rename() {
        guard !trimmedName.isEmpty else { return }
        let name = trimmedName
        Task {
            await state.perform(["rename", container.id, name])
        }
        dismiss()
    }
}

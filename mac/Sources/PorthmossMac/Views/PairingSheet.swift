import SwiftUI

/// Shown once per PC: the agent displays a code, and typing it here proves to
/// the PC that the person at this Mac is allowed to drive it.
struct PairingSheet: View {
    @EnvironmentObject private var model: AppModel
    let request: PairingRequest

    @State private var code = ""
    @FocusState private var focused: Bool

    private var isComplete: Bool {
        code.count == 6 && code.allSatisfy(\.isNumber)
    }

    var body: some View {
        VStack(spacing: 16) {
            VStack(spacing: 6) {
                Image(systemName: "lock.display")
                    .font(.system(size: 26, weight: .light))
                    .foregroundStyle(Color.accentColor)
                Text("Pair with this PC")
                    .font(.system(size: 15, weight: .semibold))
                Text("A six-digit code is showing in the agent's window on the PC.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }

            TextField("000000", text: $code)
                .textFieldStyle(.plain)
                .font(.system(size: 30, weight: .medium, design: .monospaced))
                .multilineTextAlignment(.center)
                .focused($focused)
                .padding(.vertical, 10)
                .frame(maxWidth: .infinity)
                .glassSurfaceEffect(
                    in: RoundedRectangle(cornerRadius: Metrics.panelRadius, style: .continuous)
                )
                .onChange(of: code) { _, new in
                    code = String(new.filter(\.isNumber).prefix(6))
                }
                .onSubmit { if isComplete { model.submitPairingCode(code) } }

            Panel {
                VStack(alignment: .leading, spacing: 5) {
                    Text("PC identity")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundStyle(.secondary)
                    Text(request.readableFingerprint)
                        .font(.system(size: 9, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                    Text("Porthmoss will refuse to connect if this ever changes.")
                        .font(.system(size: 10))
                        .foregroundStyle(.tertiary)
                }
            }

            HStack {
                Button("Cancel") { model.cancelPairing() }
                    .glassButtonStyle()
                    .keyboardShortcut(.cancelAction)
                Spacer()
                Button("Pair") { model.submitPairingCode(code) }
                    .glassButtonStyle(prominent: true)
                    .disabled(!isComplete)
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 340)
        .onAppear { focused = true }
    }
}

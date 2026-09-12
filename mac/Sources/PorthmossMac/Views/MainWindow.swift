import PorthmossCore
import SwiftUI

struct MainWindow: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        VStack(spacing: Metrics.gap) {
            StatusPanel()
            PCPanel()
            if model.isConnected { CrossingPanel() }
            SettingsPanel()
            Logotype()
        }
        .padding(Metrics.gap)
        .task {
            // Look for PCs as soon as the window appears: the common case is
            // one PC on the LAN, and making the user press a button first is
            // a step that never had a reason to exist.
            if model.discovered.isEmpty, model.settings.agentHost.isEmpty {
                model.search()
            }
        }
        .sheet(item: $model.pairingRequest) { request in
            PairingSheet(request: request)
                .environmentObject(model)
        }
    }
}

/// The wordmark. πορθμός is Greek for a strait — the narrow water between two
/// shores. The doubled final sigma is deliberate: it mirrors the doubled s in
/// Porthmoss, so the mark and the product name end the same way. Set quietly,
/// in half-transparent grey, so it reads as a mark rather than as another
/// piece of the interface.
private struct Logotype: View {
    var body: some View {
        Text("πορθμόςς")
            .font(.system(size: 17, weight: .light, design: .serif))
            .tracking(4)
            .foregroundStyle(Color.gray.opacity(0.5))
            .frame(maxWidth: .infinity)
            .padding(.top, 2)
            .accessibilityHidden(true)
    }
}

// A sheet needs an Identifiable binding; the request's lifetime is the identity.
extension PairingRequest: Identifiable {
    public var id: ObjectIdentifier { ObjectIdentifier(self) }
}

// MARK: - Status

private struct StatusPanel: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        Panel(tint: tint) {
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 10) {
                    ZStack {
                        Circle()
                            .fill(dotColour.opacity(0.18))
                            .frame(width: 30, height: 30)
                        Image(systemName: icon)
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(dotColour)
                    }
                    VStack(alignment: .leading, spacing: 1) {
                        Text(headline)
                            .font(.system(size: 13, weight: .semibold))
                        if !model.statusLine.isEmpty {
                            Text(model.statusLine)
                                .font(.system(size: 11))
                                .foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                    Spacer(minLength: 0)
                    if model.state.isBusy {
                        ProgressView().controlSize(.small)
                    }
                }

                if let screens = model.screens {
                    Divider().opacity(0.4)
                    DetailRow(
                        label: "PC display",
                        value: "\(screens.virtualDesktop.width) × \(screens.virtualDesktop.height)"
                            + (screens.monitors.count > 1 ? " · \(screens.monitors.count) monitors" : "")
                    )
                }
            }
        }
    }

    private var headline: String {
        switch model.state {
        case .idle: return "Not connected"
        case .searching: return "Searching for PCs"
        case let .connecting(host): return "Connecting to \(host)"
        case let .pairing(host): return "Pairing with \(host)"
        case let .connected(host):
            return model.isControllingPC ? "Controlling \(host)" : "Connected to \(host)"
        case .failed: return "Disconnected"
        }
    }

    private var icon: String {
        switch model.state {
        case .connected: return model.isControllingPC ? "arrow.right.circle.fill" : "checkmark.circle.fill"
        case .failed: return "exclamationmark.triangle.fill"
        default: return "display"
        }
    }

    private var dotColour: Color {
        switch model.state {
        case .connected: return model.isControllingPC ? .blue : .green
        case .failed: return .orange
        default: return .secondary
        }
    }

    private var tint: Color? {
        switch model.state {
        case .connected: return (model.isControllingPC ? Color.blue : .green).opacity(0.10)
        case .failed: return Color.orange.opacity(0.10)
        default: return nil
        }
    }
}

// MARK: - Which PC

private struct PCPanel: View {
    @EnvironmentObject private var model: AppModel
    @State private var manualHost = ""

    var body: some View {
        Panel {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    Text("Windows PC")
                        .font(.system(size: 12, weight: .semibold))
                    Spacer()
                    IconButton(systemName: "arrow.clockwise", help: "Search the network") {
                        model.search()
                    }
                    .disabled(model.state.isBusy)
                }

                if !model.discovered.isEmpty {
                    VStack(spacing: 4) {
                        ForEach(model.discovered, id: \.host) { agent in
                            AgentRow(agent: agent, selected: agent.host == model.settings.agentHost) {
                                model.select(agent)
                            }
                        }
                    }
                }

                HStack(spacing: 6) {
                    TextField("Address, e.g. 192.168.0.7", text: $manualHost)
                        .textFieldStyle(.plain)
                        .font(.system(size: 11, design: .monospaced))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 5)
                        .glassSurfaceEffect(
                            in: RoundedRectangle(cornerRadius: Metrics.rowRadius, style: .continuous)
                        )
                        .onSubmit(useManualHost)
                    Button("Use", action: useManualHost)
                        .glassButtonStyle()
                        .disabled(manualHost.trimmingCharacters(in: .whitespaces).isEmpty)
                }

                HStack(spacing: 8) {
                    if model.isConnected {
                        Button("Disconnect") { model.disconnect() }
                            .glassButtonStyle()
                    } else {
                        Button("Connect") { model.connect() }
                            .glassButtonStyle(prominent: true)
                            .disabled(model.state.isBusy || model.settings.agentHost.isEmpty)
                    }
                    Spacer()
                    Button("Unpair") { model.unpair() }
                        .glassButtonStyle()
                        .disabled(model.settings.agentHost.isEmpty)
                        .help("Forget the stored pairing with this PC")
                }
            }
        }
        .onAppear { manualHost = model.settings.agentHost }
    }

    private func useManualHost() {
        let host = manualHost.trimmingCharacters(in: .whitespaces)
        guard !host.isEmpty else { return }
        model.settings.agentHost = host
    }
}

private struct AgentRow: View {
    let agent: Discovery.Agent
    let selected: Bool
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Image(systemName: selected ? "largecircle.fill.circle" : "circle")
                    .font(.system(size: 11))
                    .foregroundStyle(selected ? Color.accentColor : .secondary)
                VStack(alignment: .leading, spacing: 0) {
                    Text(agent.name).font(.system(size: 11, weight: .medium))
                    Text("\(agent.host):\(agent.port)")
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 6)
            .glassSurfaceEffect(
                in: RoundedRectangle(cornerRadius: Metrics.rowRadius, style: .continuous),
                enabled: selected || hovering,
                tint: selected ? Color.accentColor.opacity(0.14) : .white.opacity(0.06)
            )
            .contentShape(RoundedRectangle(cornerRadius: Metrics.rowRadius, style: .continuous))
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
    }
}

// MARK: - How to cross

private struct CrossingPanel: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        Panel {
            VStack(alignment: .leading, spacing: 8) {
                Text("Crossing over")
                    .font(.system(size: 12, weight: .semibold))
                Label(
                    "Push the \(model.settings.capture.edge.rawValue) edge of your screen to take over the PC.",
                    systemImage: "arrow.right.to.line"
                )
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                Label(
                    "Push back at the far edge, or press ⌃⌥⌘P, to come home.",
                    systemImage: "arrow.left.to.line"
                )
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            }
        }
    }
}

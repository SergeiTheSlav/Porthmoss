import PorthmossCore
import SwiftUI

struct MainWindow: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        VStack(spacing: Metrics.gap) {
            StatusPanel()
            if !model.saved.isEmpty { SavedPanel() }
            AddPCPanel()
            if model.isConnected { CrossingPanel() }
            SettingsPanel()
            Logotype()
        }
        .padding(Metrics.gap)
        .task {
            // Reconnect to the PC most recently paired with, and otherwise go
            // looking. Opening the app and then having to press Connect every
            // time is a step that never had a reason to exist, and keying off
            // the saved list rather than settings means it never tries a PC
            // this Mac can no longer authenticate to.
            if let recent = model.saved.first {
                model.connect(to: recent.host, port: recent.port)
            } else if !model.settings.agentHost.isEmpty {
                model.connect()
            } else {
                model.search()
            }
        }
        // onDismiss is the important half. Escape, or a click outside, clears
        // the binding without going through Cancel, which left the handshake
        // blocked on a code that could no longer be typed, the status stuck on
        // "Pairing…", and no way to bring the sheet back.
        .sheet(item: $model.pairingRequest, onDismiss: { model.cancelPairing() }) { request in
            PairingSheet(request: request)
                .environmentObject(model)
                .interactiveDismissDisabled()
        }
    }
}

/// The wordmark, and the way to the credits.
///
/// πορθμός is Greek for a strait, the narrow water between two shores. The
/// doubled final sigma is deliberate: it mirrors the doubled s in Porthmoss,
/// so the mark and the product name end the same way. Set quietly, in
/// half-transparent grey, so it reads as a mark rather than as another piece
/// of the interface.
///
/// Clicking it opens the credits. That is where a wordmark leads in most Mac
/// apps, and it keeps a panel nobody opens twice out of a window that is
/// otherwise all things you came here to do.
private struct Logotype: View {
    @State private var showingAbout = false
    @State private var hovering = false

    var body: some View {
        Button {
            showingAbout = true
        } label: {
            Text("πορθμόςς")
                .font(.system(size: 17, weight: .light, design: .serif))
                .tracking(4)
                .foregroundStyle(Color.gray.opacity(hovering ? 0.8 : 0.5))
                .frame(maxWidth: .infinity)
                .padding(.top, 2)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help("About Porthmoss")
        .popover(isPresented: $showingAbout, arrowEdge: .top) {
            AboutView()
        }
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
                        if !detail.isEmpty {
                            Text(detail)
                                .font(.system(size: 11))
                                .foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                                .textSelection(.enabled)
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
        case .failed: return "Couldn’t connect"
        }
    }

    /// A failure carries its own explanation, and that explanation is the most
    /// useful thing on screen, showing only `statusLine` here meant the reason
    /// was computed and then silently dropped.
    private var detail: String {
        if case let .failed(reason) = model.state { return reason }
        return model.statusLine
    }

    private var icon: String {
        switch model.state {
        case .connected:
            return model.isControllingPC ? "arrow.right.circle.fill" : "checkmark.circle.fill"
        case .failed: return "exclamationmark.triangle.fill"
        default: return "display"
        }
    }

    private var dotColour: Color {
        switch model.state {
        case .connected:
            return model.isControllingPC ? .blue : .green
        case .failed: return .orange
        default: return .secondary
        }
    }

    private var tint: Color? {
        switch model.state {
        case .connected: return dotColour.opacity(0.10)
        case .failed: return Color.orange.opacity(0.10)
        default: return nil
        }
    }
}

// MARK: - Saved PCs

/// PCs this Mac has already paired with. One click reconnects; there is no
/// code to type and nothing to discover, which is the reason for having
/// paired in the first place.
private struct SavedPanel: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        Panel {
            VStack(alignment: .leading, spacing: 10) {
                Text("Your PCs")
                    .font(.system(size: 12, weight: .semibold))

                VStack(spacing: 4) {
                    ForEach(model.saved) { pc in
                        SavedRow(pc: pc)
                    }
                }
            }
        }
    }
}

private struct SavedRow: View {
    @EnvironmentObject private var model: AppModel
    let pc: PairingStore.SavedPC
    @State private var hovering = false

    private var isCurrent: Bool { model.settings.agentHost == pc.host }
    private var isLive: Bool { isCurrent && model.isConnected }

    var body: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(isLive ? Color.green : Color.secondary.opacity(0.35))
                .frame(width: 7, height: 7)

            VStack(alignment: .leading, spacing: 0) {
                Text(pc.displayName).font(.system(size: 11, weight: .medium))
                Text(pc.host)
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundStyle(.secondary)
            }

            Spacer(minLength: 8)

            if isLive {
                Button("Disconnect") { model.disconnect() }
                    .glassButtonStyle()
                    .controlSize(.small)
            } else {
                Button("Connect") { model.connect(to: pc.host, port: pc.port) }
                    .glassButtonStyle(prominent: true)
                    .controlSize(.small)
                    .disabled(model.state.isBusy)
            }

            IconButton(systemName: "trash", help: "Forget \(pc.displayName)") {
                model.forget(pc)
            }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .glassSurfaceEffect(
            in: RoundedRectangle(cornerRadius: Metrics.rowRadius, style: .continuous),
            enabled: isCurrent || hovering,
            tint: isLive ? Color.green.opacity(0.12)
                : (isCurrent ? Color.accentColor.opacity(0.12) : .white.opacity(0.06))
        )
        .onHover { hovering = $0 }
    }
}

// MARK: - Adding a PC

/// Pairing with something new. Kept apart from the saved list because it is a
/// different job: this one needs the PC in front of you and a code off its
/// screen, and it happens once per machine.
private struct AddPCPanel: View {
    @EnvironmentObject private var model: AppModel
    @State private var manualHost = ""

    var body: some View {
        Panel {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    Text(model.saved.isEmpty ? "Connect to a PC" : "Add another PC")
                        .font(.system(size: 12, weight: .semibold))
                    Spacer()
                    if model.state == .searching {
                        ProgressView().controlSize(.small)
                    }
                    IconButton(systemName: "arrow.clockwise", help: "Search the network") {
                        model.search()
                    }
                    .disabled(model.state.isBusy)
                }

                if model.unpaired.isEmpty {
                    Text(model.state == .searching
                         ? "Looking for PCs running Porthmoss…"
                         : "No new PCs found. Start Porthmoss on the PC, or type its address.")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                } else {
                    VStack(spacing: 4) {
                        ForEach(model.unpaired, id: \.host) { agent in
                            HStack(spacing: 8) {
                                VStack(alignment: .leading, spacing: 0) {
                                    Text(agent.name).font(.system(size: 11, weight: .medium))
                                    Text("\(agent.host):\(agent.port)")
                                        .font(.system(size: 10, design: .monospaced))
                                        .foregroundStyle(.secondary)
                                }
                                Spacer(minLength: 8)
                                Button("Pair") { model.connect(to: agent.host, port: agent.port) }
                                    .glassButtonStyle(prominent: true)
                                    .controlSize(.small)
                                    .disabled(model.state.isBusy)
                            }
                            .padding(.horizontal, 8)
                            .padding(.vertical, 6)
                            .glassSurfaceEffect(
                                in: RoundedRectangle(cornerRadius: Metrics.rowRadius, style: .continuous),
                                tint: .white.opacity(0.05)
                            )
                        }
                    }
                }

                HStack(spacing: 6) {
                    TextField("Or type an address, e.g. 192.168.0.7", text: $manualHost)
                        .textFieldStyle(.plain)
                        .font(.system(size: 11, design: .monospaced))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 5)
                        .glassSurfaceEffect(
                            in: RoundedRectangle(cornerRadius: Metrics.rowRadius, style: .continuous)
                        )
                        .onSubmit(pairManually)
                    Button("Pair", action: pairManually)
                        .glassButtonStyle()
                        .disabled(manualHost.trimmingCharacters(in: .whitespaces).isEmpty
                                  || model.state.isBusy)
                }
            }
        }
    }

    private func pairManually() {
        let host = manualHost.trimmingCharacters(in: .whitespaces)
        guard !host.isEmpty else { return }
        model.connect(to: host, port: model.settings.agentPort)
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
                Label(crossingHint, systemImage: "arrow.right.to.line")
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

    /// With two screens attached, "push the right edge" does not say which
    /// right edge, so name the screen whenever there is a choice to be made.
    private var crossingHint: String {
        let edge = model.settings.capture.edge.rawValue
        if let display = model.displays.first(where: { $0.id == model.settings.crossingDisplay }) {
            return "Push the \(edge) edge of \(display.name) to take over the PC."
        }
        if model.displays.count > 1 {
            return "Push the \(edge) edge of your outermost screen to take over the PC."
        }
        return "Push the \(edge) edge of your screen to take over the PC."
    }
}

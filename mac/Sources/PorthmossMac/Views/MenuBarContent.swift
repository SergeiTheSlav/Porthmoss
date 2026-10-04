import PorthmossCore
import SwiftUI

/// The menu bar dropdown. Deliberately thin: status, the one action that
/// matters right now, and a way into the window.
struct MenuBarContent: View {
    @EnvironmentObject private var model: AppModel
    let openMainWindow: () -> Void

    var body: some View {
        Text(headline)

        if !model.statusLine.isEmpty {
            Text(model.statusLine)
        }

        Divider()

        if model.isConnected {
            Button("Disconnect from \(model.agentLabel)") { model.disconnect() }
        } else {
            Button(model.state.isBusy ? "Connecting…" : "Connect to \(model.agentLabel)") {
                model.connect()
            }
            .disabled(model.state.isBusy || model.settings.agentHost.isEmpty)
        }

        Button("Open Porthmoss…") { openMainWindow() }
            .keyboardShortcut(",", modifiers: .command)

        Divider()

        Link("\(About.name) \(About.version), source on GitHub",
             destination: URL(string: About.repository)!)

        Button("Quit Porthmoss") { NSApplication.shared.terminate(nil) }
            .keyboardShortcut("q", modifiers: .command)
    }

    private var headline: String {
        switch model.state {
        case .idle: return "Not connected"
        case .searching: return "Searching…"
        case let .connecting(host): return "Connecting to \(host)…"
        case let .pairing(host): return "Pairing with \(host)…"
        case let .connected(host):
            return model.isControllingPC ? "Controlling \(host)" : "Connected to \(host)"
        case .failed: return "Disconnected"
        }
    }
}

import Foundation
import PorthmossCore
import SwiftUI

/// Where the app is in its lifecycle. The UI is a function of this.
enum LinkState: Equatable {
    case idle
    case searching
    case connecting(String)
    case pairing(String)
    case connected(String)
    case failed(String)

    var isBusy: Bool {
        switch self {
        case .searching, .connecting, .pairing: return true
        default: return false
        }
    }
}

/// A pairing code the connection queue is blocked waiting for.
///
/// The handshake runs off the main thread and needs the code synchronously, so
/// it parks on a semaphore while the UI collects it. Reaching for async/await
/// here would mean restructuring the handshake around an actor for no gain.
final class PairingRequest: @unchecked Sendable {
    let fingerprint: Data
    private let semaphore = DispatchSemaphore(value: 0)
    private var code: String?

    init(fingerprint: Data) { self.fingerprint = fingerprint }

    /// Called on the connection queue. Blocks until the UI answers.
    func wait() -> String? {
        semaphore.wait()
        return code
    }

    /// Called from the UI. `nil` cancels the pairing.
    func answer(_ code: String?) {
        self.code = code
        semaphore.signal()
    }

    /// The fingerprint in the grouped form shown to the user, so it can be
    /// compared against the agent's window at a glance without reading 64
    /// undifferentiated hex characters.
    var readableFingerprint: String {
        let hex = fingerprint.map { String(format: "%02X", $0) }.joined()
        return stride(from: 0, to: hex.count, by: 4).map { offset in
            let start = hex.index(hex.startIndex, offsetBy: offset)
            let end = hex.index(start, offsetBy: 4)
            return String(hex[start ..< end])
        }.joined(separator: " ")
    }
}

@MainActor
final class AppModel: ObservableObject {
    @Published private(set) var state: LinkState = .idle
    @Published private(set) var statusLine = ""
    @Published private(set) var screens: RemoteScreens?
    @Published private(set) var discovered: [Discovery.Agent] = []
    @Published private(set) var isControllingPC = false
    /// Non-nil while a pairing sheet should be on screen.
    @Published var pairingRequest: PairingRequest?

    @Published var settings: Settings {
        didSet { try? settings.save() }
    }

    init(settings: Settings) { self.settings = settings }

    private var connection: AgentConnection?
    private var session: Session?

    var agentLabel: String {
        settings.agentHost.isEmpty ? "no PC selected" : settings.agentHost
    }

    var isConnected: Bool {
        if case .connected = state { return true }
        return false
    }

    // MARK: - Discovery

    func search() {
        guard !state.isBusy else { return }
        state = .searching
        statusLine = "Looking for PCs on the network…"
        Discovery.find(timeout: 3) { [weak self] agents in
            Task { @MainActor in
                guard let self else { return }
                self.discovered = agents.sorted { $0.name < $1.name }
                if self.discovered.isEmpty {
                    self.state = .failed("No PCs found. Check the agent is running, or enter its address.")
                } else {
                    self.state = .idle
                    self.statusLine = "Found \(self.discovered.count) PC\(self.discovered.count == 1 ? "" : "s")."
                    // One obvious candidate and nothing configured: just take it.
                    if self.settings.agentHost.isEmpty, let only = self.discovered.first,
                       self.discovered.count == 1 {
                        self.settings.agentHost = only.host
                        self.settings.agentPort = only.port
                    }
                }
            }
        }
    }

    func select(_ agent: Discovery.Agent) {
        settings.agentHost = agent.host
        settings.agentPort = agent.port
    }

    // MARK: - Connection

    func connect() {
        guard !state.isBusy, !isConnected else { return }
        // Cancel anything still open first. Dropping the reference is not
        // enough: the socket stays connected and keeps the agent's single
        // controller slot, which then refuses every later attempt.
        teardown()
        let host = settings.agentHost
        guard !host.isEmpty else {
            state = .failed("Pick a PC first, or type its address.")
            return
        }

        state = .connecting(host)
        statusLine = "Connecting to \(host)…"

        let stored = PairingStore.load(agent: host)
        let connection = AgentConnection(
            host: host, port: settings.agentPort, pinnedFingerprint: stored?.fingerprint
        )
        self.connection = connection

        connection.start(
            clientName: settings.clientName,
            storedSecret: stored?.secret,
            codeProvider: { [weak self] fingerprint in
                // Runs on the connection queue. Hand the request to the UI and
                // block here until the user types the code or cancels.
                let request = PairingRequest(fingerprint: fingerprint)
                Task { @MainActor in
                    self?.state = .pairing(host)
                    self?.statusLine = "Enter the code shown on the PC."
                    self?.pairingRequest = request
                }
                return request.wait()
            },
            completion: { [weak self] result in
                Task { @MainActor in
                    self?.finishConnecting(result, host: host)
                }
            }
        )
    }

    private func finishConnecting(
        _ result: Result<(RemoteScreens, Data, Data), Error>, host: String
    ) {
        pairingRequest = nil
        switch result {
        case let .failure(error):
            state = .failed(describe(error))
            statusLine = ""
            connection?.stop()
            connection = nil

        case let .success((screens, secret, fingerprint)):
            do {
                try PairingStore.save(agent: host, secret: secret, fingerprint: fingerprint)
            } catch {
                statusLine = "Connected, but the pairing could not be saved."
            }
            self.screens = screens
            guard let connection else { return }

            let session = Session(connection: connection, screens: screens, settings: settings)
            session.onStatus = { [weak self] line in
                Task { @MainActor in self?.handleSessionStatus(line) }
            }
            do {
                try session.start()
            } catch let error as EventTap.StartError {
                state = .failed(error.description)
                teardown()
                return
            } catch {
                state = .failed(error.localizedDescription)
                teardown()
                return
            }
            self.session = session
            state = .connected(host)
            statusLine = "Push the \(settings.capture.edge.rawValue) edge to take over the PC."
        }
    }

    private func handleSessionStatus(_ line: String) {
        statusLine = line
        isControllingPC = line.hasPrefix("Controlling")
        if line.hasPrefix("Connection lost") {
            state = .failed(line)
            teardown()
        }
    }

    func disconnect() {
        teardown()
        state = .idle
        statusLine = "Disconnected."
        screens = nil
    }

    private func teardown() {
        session?.stop()
        session = nil
        // Session.stop() cancels the connection when there is a session; when
        // the handshake failed there is none, and this is the only thing that
        // closes the socket.
        connection?.stop()
        connection = nil
        isControllingPC = false
    }

    func unpair() {
        let host = settings.agentHost
        guard !host.isEmpty else { return }
        teardown()
        PairingStore.forget(agent: host)
        state = .idle
        statusLine = "Forgot the pairing with \(host). Run the agent's Unpair too, then reconnect."
    }

    func submitPairingCode(_ code: String) {
        pairingRequest?.answer(code)
        pairingRequest = nil
    }

    func cancelPairing() {
        pairingRequest?.answer(nil)
        pairingRequest = nil
    }

    private func describe(_ error: Error) -> String {
        guard let wire = error as? WireError else { return error.localizedDescription }
        switch wire {
        case .truncated: return "The PC sent a malformed message."
        case let .frameTooLarge(size): return "The PC sent an oversized message (\(size) bytes)."
        case let .unknownType(type): return "Unknown message type 0x\(String(type, radix: 16))."
        case let .versionMismatch(version):
            return "The PC speaks protocol v\(version); this Mac speaks v\(Wire.version)."
        case let .rejected(reason): return reason
        }
    }
}

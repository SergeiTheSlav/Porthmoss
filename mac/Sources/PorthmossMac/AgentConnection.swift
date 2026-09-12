import CryptoKit
import Foundation
import Network
import PorthmossCore

/// The Mac's end of the link: a pinned TLS connection to the Windows agent.
///
/// The agent's certificate is self-signed, so the usual PKI checks are
/// meaningless. Instead we pin its SHA-256 fingerprint on first pairing and
/// refuse anything else afterwards — an unauthenticated input channel is total
/// remote control of the PC, so this is the load-bearing part.
final class AgentConnection: @unchecked Sendable {
    enum State: Sendable {
        case idle, connecting, needsPairingCode, ready, failed(String)
    }

    private let connection: NWConnection
    private let queue = DispatchQueue(label: "app.porthmoss.connection")
    private var inbound = Data()
    private var pendingPong: @Sendable (UInt64) -> Void = { _ in }

    /// Frames that arrived before the session was wired up.
    ///
    /// The agent can send its first message immediately after the handshake —
    /// the probe sends ENTER about a millisecond later — while the Mac only
    /// installs its handlers once Session.start() runs. Without this those
    /// frames land on a default no-op and vanish, and losing ENTER in
    /// particular means the Mac is being driven without knowing it: its own
    /// edge detection stays live and it never releases.
    private var buffered: [(type: UInt8, body: [UInt8])] = []
    private var deliveringToSession = false
    /// Enough to cover the gap; a peer that floods before we are ready is not
    /// something to absorb indefinitely.
    private static let maxBuffered = 256

    /// The fingerprint the server actually presented, captured during the TLS
    /// handshake so pairing can salt with it.
    private var presentedFingerprint: Data?
    private let pinnedFingerprint: Data?

    private(set) var screens: RemoteScreens?

    var onStateChange: @Sendable (State) -> Void = { _ in }
    var onDisconnect: @Sendable (String) -> Void = { _ in }
    /// Text the PC copied.
    var onClipboardText: @Sendable (String) -> Void = { _ in }
    /// An input frame from the PC, when it is driving this Mac.
    var onInputFrame: @Sendable (UInt8, [UInt8]) -> Void = { _, _ in }
    /// A file-transfer frame from the PC: type and body, applied in order.
    var onFileFrame: @Sendable (UInt8, [UInt8]) -> Void = { _, _ in }

    private let host: String
    private let port: UInt16

    init(host: String, port: UInt16, pinnedFingerprint: Data?) {
        self.pinnedFingerprint = pinnedFingerprint
        self.host = host
        self.port = port

        let tls = NWProtocolTLS.Options()
        sec_protocol_options_set_min_tls_protocol_version(tls.securityProtocolOptions, .TLSv13)

        let box = FingerprintBox()
        sec_protocol_options_set_verify_block(
            tls.securityProtocolOptions,
            { _, trustRef, complete in
                guard let chain = SecTrustCopyCertificateChain(sec_trust_copy_ref(trustRef).takeRetainedValue()),
                      CFArrayGetCount(chain) > 0
                else { return complete(false) }

                let cert = unsafeBitCast(CFArrayGetValueAtIndex(chain, 0), to: SecCertificate.self)
                let der = SecCertificateCopyData(cert) as Data
                let fingerprint = Data(SHA256.hash(data: der))
                box.value = fingerprint

                if let pinned = box.pinned {
                    // Constant-time compare: a fingerprint mismatch means a
                    // different machine is answering, and we must not connect.
                    let matches = constantTimeEquals(pinned, fingerprint)
                    box.rejectedPin = !matches
                    complete(matches)
                } else {
                    // First pairing: trust on first use, then show the user the
                    // fingerprint so they can confirm which PC they just trusted.
                    complete(true)
                }
            },
            queue
        )
        box.pinned = pinnedFingerprint
        self.fingerprintBox = box

        let params = NWParameters(tls: tls, tcp: {
            let tcp = NWProtocolTCP.Options()
            // Input events are tiny and latency-critical; Nagle would batch
            // them into visible cursor stutter.
            tcp.noDelay = true
            tcp.connectionTimeout = 5
            return tcp
        }())

        connection = NWConnection(
            host: NWEndpoint.Host(host),
            port: NWEndpoint.Port(rawValue: port)!,
            using: params
        )
    }

    private let fingerprintBox: FingerprintBox

    /// Turns a network failure into something a user can act on. "NWError
    /// error 61" tells nobody anything; the interesting cases are a rejected
    /// pin, and nothing listening at the other end.
    private func explain(_ error: NWError) -> Error {
        if case let .posix(code) = error, !fingerprintBox.rejectedPin {
            switch code {
            case .ECONNREFUSED:
                return WireError.rejected(
                    "Nothing is listening at \(host):\(port). Is Porthmoss running on the PC?")
            case .ETIMEDOUT, .EHOSTUNREACH, .EHOSTDOWN:
                return WireError.rejected(
                    "\(host) didn’t answer. Check the PC is awake, on the same network, "
                        + "and allowed through its firewall.")
            case .ENETUNREACH, .ENETDOWN:
                return WireError.rejected("This Mac has no route to \(host).")
            case .ECONNRESET:
                return WireError.rejected("The PC closed the connection.")
            default:
                return error
            }
        }
        guard fingerprintBox.rejectedPin else { return error }
        // Short on purpose: this is rendered in a small panel, and two 64-digit
        // fingerprints inline crowded out the sentence that actually tells the
        // user what to do.
        return WireError.rejected("""
        \(host) is not the PC this Mac paired with — it presented a different \
        identity.

        If you reset Porthmoss on that PC, forget it here and pair again. If \
        you did not, do not pair: something else may be answering on that \
        address, and it should not be trusted with your keyboard.
        """)
    }

    /// Connects and completes the handshake. `codeProvider` is asked for the
    /// 6-digit pairing code only when the agent says it has never been paired.
    /// It receives the certificate fingerprint actually presented, so the UI
    /// can show the user which machine they are about to trust.
    func start(
        clientName: String,
        storedSecret: Data?,
        codeProvider: @escaping @Sendable (Data) -> String?,
        completion: @escaping @Sendable (Result<(RemoteScreens, Data, Data), Error>) -> Void
    ) {
        onStateChange(.connecting)
        connection.stateUpdateHandler = { [weak self] state in
            guard let self else { return }
            switch state {
            case .ready:
                self.handshake(
                    clientName: clientName,
                    storedSecret: storedSecret,
                    codeProvider: codeProvider,
                    completion: completion
                )
            case let .failed(error):
                completion(.failure(self.explain(error)))
            case let .waiting(error):
                // Usually the agent is not running or a firewall is in the way.
                completion(.failure(self.explain(error)))
            default:
                break
            }
        }
        connection.start(queue: queue)
    }

    func stop() {
        connection.cancel()
    }

    // MARK: - Handshake

    private func handshake(
        clientName: String,
        storedSecret: Data?,
        codeProvider: @escaping @Sendable (Data) -> String?,
        completion: @escaping @Sendable (Result<(RemoteScreens, Data, Data), Error>) -> Void
    ) {
        do {
            try sendFrame(.hello, Wire.helloBody(
                name: clientName, needsPairing: storedSecret == nil
            ))
        } catch {
            return completion(.failure(error))
        }

        readFrame { [weak self] result in
            guard let self else { return }
            switch result {
            case let .failure(error):
                completion(.failure(error))
            case let .success(frame):
                // The agent refuses some connections outright — a second Mac,
                // say — and says why. Reporting "expected CHALLENGE" instead
                // of its message helps nobody.
                if frame.type == Wire.MessageType.error.rawValue {
                    return completion(.failure(WireError.rejected(
                        String(decoding: frame.body, as: UTF8.self))))
                }
                guard frame.type == Wire.MessageType.challenge.rawValue else {
                    return completion(.failure(WireError.rejected(
                        "The PC sent an unexpected reply. Is it running a different version?")))
                }
                do {
                    let (nonce, needsPairing) = try Wire.decodeChallenge(frame.body)
                    guard let fingerprint = self.fingerprintBox.value else {
                        return completion(.failure(WireError.rejected("no certificate presented")))
                    }

                    let secret: Data
                    if needsPairing {
                        self.onStateChange(.needsPairingCode)
                        guard let code = codeProvider(fingerprint) else {
                            return completion(.failure(WireError.rejected("pairing cancelled")))
                        }
                        secret = Pairing.deriveSecret(code: code, fingerprint: fingerprint)
                    } else {
                        guard let stored = storedSecret else {
                            return completion(.failure(WireError.rejected(
                                "This Mac isn’t paired with that PC. On the PC, click "
                                    + "“Forget paired Mac” in Porthmoss, then connect again "
                                    + "to pair with a new code.")))
                        }
                        secret = stored
                    }

                    let proof = Pairing.sign(secret: secret, nonce: Data(nonce))
                    try self.sendFrame(.auth, [UInt8](proof))

                    self.readFrame { readyResult in
                        switch readyResult {
                        case let .failure(error):
                            completion(.failure(error))
                        case let .success(ready):
                            if ready.type == Wire.MessageType.error.rawValue {
                                let message = String(decoding: ready.body, as: UTF8.self)
                                return completion(.failure(WireError.rejected(message)))
                            }
                            guard ready.type == Wire.MessageType.ready.rawValue else {
                                return completion(.failure(WireError.rejected("expected READY from agent")))
                            }
                            do {
                                let screens = try RemoteScreens.decode(ready.body)
                                self.screens = screens
                                self.onStateChange(.ready)
                                self.pump()
                                completion(.success((screens, secret, fingerprint)))
                            } catch {
                                completion(.failure(error))
                            }
                        }
                    }
                } catch {
                    completion(.failure(error))
                }
            }
        }
    }

    // MARK: - Sending

    func sendFrame(_ type: Wire.MessageType, _ body: [UInt8] = []) throws {
        let data = try Wire.frame(type, body)
        connection.send(content: data, completion: .contentProcessed { [weak self] error in
            if let error { self?.onDisconnect("send failed: \(error.localizedDescription)") }
        })
    }

    /// Fire-and-forget send for the input hot path, where a thrown error would
    /// only ever mean the connection is already gone.
    func post(_ type: Wire.MessageType, _ body: [UInt8] = []) {
        try? sendFrame(type, body)
    }

    // MARK: - Receiving

    private func readFrame(_ completion: @escaping @Sendable (Result<(type: UInt8, body: [UInt8]), Error>) -> Void) {
        if let frame = try? Wire.nextFrame(from: &inbound) {
            return completion(.success(frame))
        }
        connection.receive(minimumIncompleteLength: 1, maximumLength: 8192) { [weak self] data, _, isComplete, error in
            guard let self else { return }
            if let error { return completion(.failure(error)) }
            if let data, !data.isEmpty { self.inbound.append(data) }
            if isComplete, self.inbound.isEmpty {
                return completion(.failure(WireError.rejected("agent closed the connection")))
            }
            self.readFrame(completion)
        }
    }

    /// Keeps reading after the handshake, so PONGs arrive and a closed socket
    /// is noticed promptly.
    private func pump() {
        readFrame { [weak self] result in
            guard let self else { return }
            switch result {
            case let .failure(error):
                self.onDisconnect(error.localizedDescription)
            case let .success(frame):
                // PONG drives the dead-man switch and must never be delayed.
                if frame.type == Wire.MessageType.pong.rawValue {
                    if let id = try? Wire.decodeU64(frame.body) { self.pendingPong(id) }
                } else if self.deliveringToSession {
                    self.deliver(frame.type, frame.body)
                } else if self.buffered.count < AgentConnection.maxBuffered {
                    self.buffered.append((frame.type, frame.body))
                }
                self.pump()
            }
        }
    }

    /// Hands one frame to whichever part of the session owns it.
    private func deliver(_ type: UInt8, _ body: [UInt8]) {
        switch type {
        case Wire.MessageType.clipboardText.rawValue:
            onClipboardText(String(decoding: body, as: UTF8.self))
        case Wire.MessageType.mouseMove.rawValue,
             Wire.MessageType.mouseButton.rawValue,
             Wire.MessageType.mouseWheel.rawValue,
             Wire.MessageType.key.rawValue,
             Wire.MessageType.keyReset.rawValue,
             Wire.MessageType.enter.rawValue,
             Wire.MessageType.leave.rawValue:
            onInputFrame(type, body)
        case Wire.MessageType.fileBegin.rawValue,
             Wire.MessageType.fileChunk.rawValue,
             Wire.MessageType.fileEnd.rawValue,
             Wire.MessageType.fileAbort.rawValue:
            onFileFrame(type, body)
        default:
            break
        }
    }

    func onPong(_ handler: @escaping @Sendable (UInt64) -> Void) {
        pendingPong = handler
    }

    /// Says the session's handlers are installed, and releases anything that
    /// arrived in the meantime, in order.
    func beginDelivery() {
        queue.async { [weak self] in
            guard let self else { return }
            self.deliveringToSession = true
            let pending = self.buffered
            self.buffered = []
            for frame in pending {
                self.deliver(frame.type, frame.body)
            }
        }
    }
}

/// Shuttles the observed certificate fingerprint out of the C verify block.
private final class FingerprintBox: @unchecked Sendable {
    var value: Data?
    var pinned: Data?
    var rejectedPin = false
}

private func hex(_ data: Data) -> String {
    data.map { String(format: "%02x", $0) }.joined()
}

private func constantTimeEquals(_ a: Data, _ b: Data) -> Bool {
    guard a.count == b.count else { return false }
    var difference: UInt8 = 0
    for (x, y) in zip(a, b) { difference |= x ^ y }
    return difference == 0
}

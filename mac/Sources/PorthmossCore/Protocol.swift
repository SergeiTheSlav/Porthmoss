import Foundation

/// The Porthmoss v1 wire protocol. Mirrors `win/internal/proto` — see
/// `docs/protocol.md`. Everything is big-endian.
public enum Wire {
    public static let version: UInt16 = 1
    /// Generous because of the clipboard: input events are a few bytes each,
    /// a pasted document is not, and chunking would buy nothing over a link
    /// that already carries the whole thing in one write.
    public static let maxFrame = 1 << 20
    public static let nonceSize = 32
    public static let macSize = 32

    public enum MessageType: UInt8 {
        case hello = 0x01
        case challenge = 0x02
        case auth = 0x03
        case ready = 0x04
        case error = 0x05
        /// The mirror of READY: the Mac describing itself to the agent.
        case clientInfo = 0x06

        case mouseMove = 0x10
        case mouseButton = 0x11
        case mouseWheel = 0x12
        case key = 0x20
        case keyReset = 0x21

        case enter = 0x30
        case leave = 0x31
        case ping = 0x40
        case pong = 0x41

        /// UTF-8 text, in either direction.
        case clipboardText = 0x50

        /// File transfer: BEGIN, then CHUNK until the declared size has
        /// arrived, then END. ABORT may replace END at any point.
        case fileBegin = 0x60
        case fileChunk = 0x61
        case fileEnd = 0x62
        case fileAbort = 0x63
    }

    public enum MouseButton: UInt8 {
        case left = 1, right = 2, middle = 3, x1 = 4, x2 = 5
    }

    /// Set if the scancode needs an E0 prefix on Windows (arrows, right-hand
    /// modifiers, numpad enter and friends).
    public static let keyFlagExtended: UInt8 = 1 << 0
}

public enum WireError: Error, Equatable, LocalizedError {
    case truncated
    case frameTooLarge(Int)
    case unknownType(UInt8)
    case versionMismatch(UInt16)
    case rejected(String)

    /// Without this, anything that falls back to `localizedDescription` — a
    /// dropped connection, say — surfaces as "WireError error 3", which tells
    /// the user nothing at all.
    public var errorDescription: String? {
        switch self {
        case .truncated:
            return "The PC sent a malformed message."
        case let .frameTooLarge(size):
            return "The PC sent an oversized message (\(size) bytes)."
        case let .unknownType(type):
            return "The PC sent an unknown message type 0x\(String(type, radix: 16))."
        case let .versionMismatch(version):
            return "The PC speaks protocol v\(version); this Mac speaks v\(Wire.version)."
        case let .rejected(reason):
            return reason
        }
    }
}

// MARK: - Framing

public extension Wire {
    /// Encodes one message as `u32 length | u8 type | body`.
    static func frame(_ type: MessageType, _ body: [UInt8] = []) throws -> Data {
        let length = 1 + body.count
        guard length <= maxFrame else { throw WireError.frameTooLarge(length) }
        var out = Data(capacity: 4 + length)
        out.appendBigEndian(UInt32(length))
        out.append(type.rawValue)
        out.append(contentsOf: body)
        return out
    }

    /// Splits a complete frame out of `buffer`, returning nil if more bytes are
    /// needed. Consumed bytes are removed from `buffer`.
    static func nextFrame(from buffer: inout Data) throws -> (type: UInt8, body: [UInt8])? {
        guard buffer.count >= 4 else { return nil }
        let length = Int(buffer.readBigEndianUInt32(at: 0))
        guard length >= 1, length <= maxFrame else { throw WireError.frameTooLarge(length) }
        guard buffer.count >= 4 + length else { return nil }

        let type = buffer[buffer.startIndex + 4]
        let bodyStart = buffer.startIndex + 5
        let body = [UInt8](buffer[bodyStart ..< bodyStart + length - 1])
        buffer.removeFirst(4 + length)
        return (type, body)
    }
}

// MARK: - Message bodies

public extension Wire {
    /// Set when this Mac holds no secret for the agent and is asking to pair
    /// again. Without it, a Mac that has lost its half of the pairing is stuck:
    /// the agent rejects it and the only way back is unpairing at the PC.
    static let helloFlagNeedsPairing: UInt8 = 1 << 0

    static let clientInfoFlagReverseControl: UInt8 = 1 << 0

    /// CLIENT_INFO: this Mac's desktop, which edge of the *PC's* desktop this
    /// Mac lies beyond, and whether it accepts being driven.
    ///
    /// Without it the agent guesses — it assumed the Mac's desktop matched its
    /// own and took the edge from a command-line flag defaulting to "left". A
    /// wrong guess is silent: the crossing simply never fires, whichever edge
    /// is pushed.
    static func clientInfoBody(
        desktop: Rect, macBeyondEdge: ScreenEdge, reverseControl: Bool
    ) -> [UInt8] {
        var out: [UInt8] = []
        for value in [desktop.x, desktop.y, desktop.width, desktop.height] {
            out.appendBigEndian(UInt32(bitPattern: Int32(value.rounded())))
        }
        out.append(reverseControl ? clientInfoFlagReverseControl : 0)
        let edge = Array(macBeyondEdge.rawValue.utf8)
        out.append(UInt8(edge.count))
        out.append(contentsOf: edge)
        return out
    }

    static func helloBody(name: String, needsPairing: Bool = false) -> [UInt8] {
        let nameBytes = Array(name.utf8.prefix(maxFrame - 8))
        var out: [UInt8] = []
        out.appendBigEndian(version)
        out.appendBigEndian(UInt16(nameBytes.count))
        out.append(contentsOf: nameBytes)
        out.append(needsPairing ? helloFlagNeedsPairing : 0)
        return out
    }

    /// CHALLENGE carries a nonce plus a flag saying the agent is showing a
    /// pairing code because it has never been paired.
    static func decodeChallenge(_ body: [UInt8]) throws -> (nonce: [UInt8], needsPairing: Bool) {
        guard body.count >= nonceSize + 1 else { throw WireError.truncated }
        return (Array(body[0 ..< nonceSize]), body[nonceSize] == 1)
    }

    /// Absolute position, normalised to 0...65535 across the virtual desktop.
    static func mouseMoveBody(x: UInt16, y: UInt16) -> [UInt8] {
        var out: [UInt8] = []
        out.appendBigEndian(x)
        out.appendBigEndian(y)
        return out
    }

    static func mouseButtonBody(_ button: MouseButton, down: Bool) -> [UInt8] {
        [button.rawValue, down ? 1 : 0]
    }

    /// Scroll in whole wheel notches; positive `dy` scrolls up.
    static func mouseWheelBody(dx: Int16, dy: Int16) -> [UInt8] {
        var out: [UInt8] = []
        out.appendBigEndian(UInt16(bitPattern: dx))
        out.appendBigEndian(UInt16(bitPattern: dy))
        return out
    }

    static func keyBody(scancode: UInt16, down: Bool, extended: Bool) -> [UInt8] {
        var out: [UInt8] = []
        out.appendBigEndian(scancode)
        out.append(down ? 1 : 0)
        out.append(extended ? keyFlagExtended : 0)
        return out
    }

    /// FILE_BEGIN: the name, the size, and where this file sits in the batch
    /// so the receiver can say "2 of 5" rather than counting.
    /// Set when the batch came from the sender's clipboard rather than a drag.
    /// The receiver writes the files either way — they have to go somewhere —
    /// but a clipboard batch also lands on its clipboard, so the next paste
    /// produces the files rather than nothing.
    static let fileFlagClipboard: UInt8 = 1 << 0

    static func fileBeginBody(
        name: String, size: UInt64, index: Int, total: Int, fromClipboard: Bool = false
    ) -> [UInt8] {
        let nameBytes = Array(name.utf8.prefix(1024))
        var out: [UInt8] = []
        out.appendBigEndian(UInt16(nameBytes.count))
        out.append(contentsOf: nameBytes)
        out.appendBigEndian(size)
        out.appendBigEndian(UInt16(clamping: index))
        out.appendBigEndian(UInt16(clamping: total))
        out.append(fromClipboard ? fileFlagClipboard : 0)
        return out
    }

    /// Decodes a FILE_BEGIN sent by the PC.
    static func decodeFileBegin(
        _ body: [UInt8]
    ) throws -> (name: String, size: UInt64, index: Int, total: Int, fromClipboard: Bool) {
        guard body.count >= 2 else { throw WireError.truncated }
        let nameLength = Int(body[0]) << 8 | Int(body[1])
        guard body.count >= 2 + nameLength + 12 else { throw WireError.truncated }
        let name = String(decoding: body[2 ..< 2 + nameLength], as: UTF8.self)
        let rest = Array(body[(2 + nameLength)...])
        let size = rest.prefix(8).reduce(UInt64(0)) { $0 << 8 | UInt64($1) }
        let index = Int(rest[8]) << 8 | Int(rest[9])
        let total = Int(rest[10]) << 8 | Int(rest[11])
        // The flag byte came after the first version of this message.
        let fromClipboard = rest.count > 12 && rest[12] & fileFlagClipboard != 0
        return (name, size, index, total, fromClipboard)
    }

    static func pingBody(_ id: UInt64) -> [UInt8] {
        var out: [UInt8] = []
        out.appendBigEndian(id)
        return out
    }

    static func decodeMouseMove(_ body: [UInt8]) throws -> (x: UInt16, y: UInt16) {
        guard body.count >= 4 else { throw WireError.truncated }
        return (UInt16(body[0]) << 8 | UInt16(body[1]), UInt16(body[2]) << 8 | UInt16(body[3]))
    }

    static func decodeMouseButton(_ body: [UInt8]) throws -> (button: UInt8, down: Bool) {
        guard body.count >= 2 else { throw WireError.truncated }
        return (body[0], body[1] == 1)
    }

    static func decodeMouseWheel(_ body: [UInt8]) throws -> (dx: Int16, dy: Int16) {
        guard body.count >= 4 else { throw WireError.truncated }
        return (
            Int16(bitPattern: UInt16(body[0]) << 8 | UInt16(body[1])),
            Int16(bitPattern: UInt16(body[2]) << 8 | UInt16(body[3]))
        )
    }

    static func decodeKey(_ body: [UInt8]) throws -> (scancode: UInt16, down: Bool, extended: Bool) {
        guard body.count >= 4 else { throw WireError.truncated }
        return (
            UInt16(body[0]) << 8 | UInt16(body[1]),
            body[2] == 1,
            body[3] & keyFlagExtended != 0
        )
    }

    static func decodeU64(_ body: [UInt8]) throws -> UInt64 {
        guard body.count >= 8 else { throw WireError.truncated }
        return body.prefix(8).reduce(UInt64(0)) { $0 << 8 | UInt64($1) }
    }
}

// MARK: - Screen layout

/// One Windows display, in virtual-desktop pixels.
public struct RemoteMonitor: Equatable, Sendable {
    public var left: Int32, top: Int32, width: Int32, height: Int32
    public var isPrimary: Bool

    public init(left: Int32, top: Int32, width: Int32, height: Int32, isPrimary: Bool = false) {
        self.left = left; self.top = top; self.width = width; self.height = height
        self.isPrimary = isPrimary
    }

    public var right: Int32 { left + width }
    public var bottom: Int32 { top + height }
}

/// The agent's display layout, as sent in READY.
public struct RemoteScreens: Equatable, Sendable {
    public var virtualDesktop: RemoteMonitor
    public var monitors: [RemoteMonitor]

    public init(virtualDesktop: RemoteMonitor, monitors: [RemoteMonitor]) {
        self.virtualDesktop = virtualDesktop
        self.monitors = monitors
    }

    public static func decode(_ body: [UInt8]) throws -> RemoteScreens {
        guard body.count >= 17 else { throw WireError.truncated }
        let virtual = RemoteMonitor(
            left: body.readBigEndianInt32(at: 0),
            top: body.readBigEndianInt32(at: 4),
            width: body.readBigEndianInt32(at: 8),
            height: body.readBigEndianInt32(at: 12)
        )
        let count = Int(body[16])
        guard body.count >= 17 + count * 17 else { throw WireError.truncated }

        var monitors: [RemoteMonitor] = []
        monitors.reserveCapacity(count)
        for i in 0 ..< count {
            let base = 17 + i * 17
            monitors.append(RemoteMonitor(
                left: body.readBigEndianInt32(at: base),
                top: body.readBigEndianInt32(at: base + 4),
                width: body.readBigEndianInt32(at: base + 8),
                height: body.readBigEndianInt32(at: base + 12),
                isPrimary: body[base + 16] == 1
            ))
        }
        return RemoteScreens(virtualDesktop: virtual, monitors: monitors)
    }
}

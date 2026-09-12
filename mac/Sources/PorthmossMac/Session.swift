import AppKit
import CoreGraphics
import Foundation
import PorthmossCore

/// Ties the event tap, the crossing model and the connection together.
///
/// Everything here runs on the main run loop, which is also where the event tap
/// callback fires — so no locking, and no latency spent hopping queues on the
/// input hot path.
// Unchecked because every member is touched only from the main run loop — the
// event tap callback fires there, and the connection's callbacks hop back to it
// before touching anything here.
final class Session: @unchecked Sendable {
    private let connection: AgentConnection
    private let model: CaptureModel
    private var settings: Settings
    private var tap: EventTap?

    private var displays: [Rect] = []
    private var desktop = Rect(x: 0, y: 0, width: 0, height: 0)

    /// Continuous trackpad scrolling arrives in pixels; Windows wants notches.
    private var scrollRemainderX = 0.0
    private var scrollRemainderY = 0.0

    private var clipboard: ClipboardBridge?
    private let incoming = FileTransfer.Receiver()
    private var pingTimer: Timer?
    private var lastPongAt = Date()
    private var nextPingID: UInt64 = 1

    var onEvent: (SessionEvent) -> Void = { print($0.message) }

    init(connection: AgentConnection, screens: RemoteScreens, settings: Settings) {
        self.connection = connection
        self.settings = settings
        self.model = CaptureModel(
            config: settings.capture,
            screens: screens,
            localDisplay: Rect(x: 0, y: 0, width: 0, height: 0)
        )
        refreshDisplays()
    }

    func start() throws {
        let tap = EventTap { [weak self] type, event in
            self?.handle(type: type, event: event) ?? false
        }
        tap.onTimeout = { [weak self] count in
            // The tap only stalls if this callback is slow. If it ever shows up
            // in the field, the input path needs to get off the callback thread.
            self?.onEvent(.note("warning: event tap stalled and was re-enabled (\(count)x) — "
                + "some input may have reached the Mac instead of the PC"))
        }
        try tap.start()
        self.tap = tap

        connection.onPong { [weak self] _ in
            DispatchQueue.main.async { self?.lastPongAt = Date() }
        }
        connection.onDisconnect = { [weak self] reason in
            DispatchQueue.main.async { self?.panic("connection lost: \(reason)") }
        }
        let clipboard = ClipboardBridge(
            send: { [weak self] text in
                self?.connection.post(.clipboardText, Array(text.utf8))
            },
            sendFiles: { [weak self] urls in
                // Copying files in Finder sends them to the PC, where they
                // land in a folder and go on its clipboard ready to paste.
                self?.sendFiles(urls, fromClipboard: true)
            }
        )
        connection.onClipboardText = { [weak self] text in
            DispatchQueue.main.async { self?.clipboard?.applyRemote(text) }
        }
        clipboard.start()
        self.clipboard = clipboard

        connection.onFileFrame = { [weak self] type, body in
            DispatchQueue.main.async { self?.receiveFileFrame(type, body) }
        }

        // Every handler is installed; release anything the agent sent while we
        // were still setting up.
        connection.beginDelivery()

        startHeartbeat()
        onEvent(.ready("Ready. Push the \(settings.capture.edge.rawValue) edge to take over the PC."))
    }


    /// Takes new settings without tearing the link down.
    ///
    /// Everything here is applied on the Mac — which edge leads to the PC, how
    /// hard to push, the modifier mapping — so there is nothing to renegotiate
    /// with the agent and no reason to make the user reconnect.
    func apply(_ new: Settings) {
        // Changing the edge while the PC is being driven would leave the
        // cursor with no way home that matches what the user just chose.
        if new.capture.edge != settings.capture.edge, model.isRemote {
            releaseControl(model.forceReturn(), announce: false)
            onEvent(.stoppedDrivingPC("Settings applied — control returned to the Mac."))
        }
        settings = new
        model.config = new.capture
    }

    func stop() {
        clipboard?.stop()
        clipboard = nil
        pingTimer?.invalidate()
        if model.isRemote { releaseControl(model.forceReturn(), announce: false) }
        tap?.stop()
        connection.stop()
    }

    // MARK: - Heartbeat

    private func startHeartbeat() {
        // Ping unconditionally, not just while driving Windows: the agent drops
        // a connection that has gone quiet for 2 s, so an idle Mac still has to
        // prove the link is alive.
        pingTimer = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { [weak self] _ in
            guard let self else { return }
            self.connection.post(.ping, Wire.pingBody(self.nextPingID))
            self.nextPingID &+= 1

            // The dead-man switch, in both directions. Whichever machine is
            // driving, a link that has gone quiet must not leave the user
            // stranded — and the two failures are not symmetric in how bad
            // they are. Driving the PC and losing the link leaves the Mac
            // without a cursor; *being* driven and losing the link leaves
            // whatever the PC was holding down held, so every key the user
            // presses afterwards does the wrong thing.
            guard Date().timeIntervalSince(self.lastPongAt) > 2.0 else { return }
            if self.model.isRemote {
                self.panic("agent stopped responding")
            }
        }
    }


    private func panic(_ reason: String) {
        if model.isRemote {
            releaseControl(model.forceReturn(), announce: false)
            onEvent(.stoppedDrivingPC("Control returned to the Mac — \(reason)."))
        } else {
            onEvent(.note("\(reason.prefix(1).uppercased())\(reason.dropFirst())."))
        }
    }

    // MARK: - Event handling

    /// Returns true when the event should be swallowed.
    private func handle(type: CGEventType, event: CGEvent) -> Bool {
        switch type {
        case .mouseMoved, .leftMouseDragged, .rightMouseDragged, .otherMouseDragged:
            return handleMotion(event, dragging: type == .leftMouseDragged)

        case .leftMouseDown, .leftMouseUp, .rightMouseDown, .rightMouseUp,
             .otherMouseDown, .otherMouseUp:
            return handleButton(type: type, event: event)

        case .scrollWheel:
            return handleScroll(event)

        case .keyDown, .keyUp:
            return handleKey(event, down: type == .keyDown)

        case .flagsChanged:
            return handleModifier(event)

        default:
            return false
        }
    }

    /// Set PORTHMOSS_TRACE=1 to print every motion decision. The crossing
    /// rules are timing- and delta-dependent, so guessing at them from the
    /// outside does not work.
    private static let trace = ProcessInfo.processInfo.environment["PORTHMOSS_TRACE"] == "1"
    private var traced = 0

    private func handleMotion(_ event: CGEvent, dragging: Bool = false) -> Bool {
        let delta = Point(
            x: Double(event.getIntegerValueField(.mouseEventDeltaX)),
            y: Double(event.getIntegerValueField(.mouseEventDeltaY))
        )
        let cursor = Point(x: event.location.x, y: event.location.y)

        if !model.isRemote {
            // Only the outer rim of the whole Mac desktop leads to Windows —
            // the boundary between two Mac displays must stay a normal
            // display boundary.
            guard let display = Displays.containing(cursor, in: displays),
                  isOuterEdge(of: display) else { return false }
            model.localDisplay = display
        }

        let action = model.mouseMoved(cursor: cursor, delta: delta, now: Date().timeIntervalSinceReferenceDate)
        if Session.trace, traced < 80 {
            traced += 1
            print(String(format: "  trace %02d remote=%@ cursor=(%.0f,%.0f) delta=(%.0f,%.0f) -> %@",
                         traced, model.isRemote ? "Y" : "n",
                         cursor.x, cursor.y, delta.x, delta.y, "\(action)"))
        }
        switch action {
        case .none:
            return false
        case let .enterRemote(x, y):
            CursorControl.capture(parkingAt: cursor)
            connection.post(.enter, Wire.mouseMoveBody(x: x, y: y))
            onEvent(.startedDrivingPC)
            // Dragging files across the edge sends them. There is no way to
            // hand a real drop to Windows from here, so they land in a folder
            // on the PC and it says so.
            if dragging {
                let files = Session.filesBeingDragged()
                if !files.isEmpty { sendFiles(files) }
            }
            return true
        case let .moveRemote(x, y):
            // Undo whatever the window server did with this movement before
            // anyone can see it: swallowing the event does not hold the cursor.
            CursorControl.pin()
            connection.post(.mouseMove, Wire.mouseMoveBody(x: x, y: y))
            return true
        case .returnToLocal:
            return releaseControl(action)
        }
    }

    /// `announce` is false when the caller reports the release itself, so a
    /// dropped connection does not produce two messages about the same event.
    @discardableResult
    /// The files in the drag currently under the cursor, if it is a file drag.
    ///
    /// macOS has no way to ask "is a drag in progress" from outside the app
    /// that started it, but the drag pasteboard holds the payload for the
    /// duration, and a left-button drag crossing the edge with file URLs on it
    /// is that question answered well enough.
    private static func filesBeingDragged() -> [URL] {
        let pasteboard = NSPasteboard(name: .drag)
        let options: [NSPasteboard.ReadingOptionKey: Any] = [
            .init(rawValue: NSPasteboard.ReadingOptionKey.urlReadingFileURLsOnly.rawValue): true,
        ]
        let found = pasteboard.readObjects(forClasses: [NSURL.self], options: options)
        return (found as? [URL]) ?? []
    }

    /// Streams files to the PC off the main thread. Reading a large file in
    /// the event tap callback would stall every input event behind it.
    private func sendFiles(_ urls: [URL], fromClipboard: Bool = false) {
        onEvent(.note(urls.count == 1
                 ? "Sending \(urls[0].lastPathComponent)…"
                 : "Sending \(urls.count) files…"))
        let connection = self.connection
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            for (index, url) in urls.enumerated() {
                do {
                    try FileTransfer.send(
                        url, index: index, total: urls.count, fromClipboard: fromClipboard
                    ) { type, body in
                        connection.post(type, body)
                    }
                } catch {
                    connection.post(.fileAbort, Array(error.localizedDescription.utf8))
                    DispatchQueue.main.async {
                        self?.onEvent(.note(error.localizedDescription))
                    }
                    return
                }
            }
            DispatchQueue.main.async {
                self?.onEvent(.note(urls.count == 1
                               ? "Sent \(urls[0].lastPathComponent) to the PC."
                               : "Sent \(urls.count) files to the PC."))
            }
        }
    }


    /// Applies one file-transfer frame from the PC.
    private func receiveFileFrame(_ type: UInt8, _ body: [UInt8]) {
        do {
            switch type {
            case Wire.MessageType.fileBegin.rawValue:
                let begin = try Wire.decodeFileBegin(body)
                try incoming.begin(
                    name: begin.name, size: begin.size,
                    index: begin.index, total: begin.total,
                    fromClipboard: begin.fromClipboard
                )
            case Wire.MessageType.fileChunk.rawValue:
                try incoming.chunk(body)
            case Wire.MessageType.fileEnd.rawValue:
                let url = try incoming.end()
                // A copy becomes a paste: the whole batch goes on the
                // pasteboard once the last file has landed.
                if let batch = incoming.completedClipboardBatch {
                    clipboard?.applyRemoteFiles(batch)
                    onEvent(.note(batch.count == 1
                             ? "\(url.lastPathComponent) is ready to paste."
                             : "\(batch.count) files are ready to paste."))
                } else {
                    onEvent(.note("Received \(url.lastPathComponent) from the PC."))
                }
            default:
                incoming.discard()
            }
        } catch {
            incoming.discard()
            onEvent(.note("File from the PC failed: \(error.localizedDescription)"))
        }
    }

    private func releaseControl(
        _ action: CaptureAction?, announce: Bool = true,
        origin: String = #function, line: Int = #line
    ) -> Bool {
        guard case let .returnToLocal(point)? = action else { return false }
        if Session.trace {
            print("  trace RELEASE from \(origin):\(line)")
        }
        connection.post(.leave)
        CursorControl.release(to: point)
        if announce { onEvent(.stoppedDrivingPC("Back on the Mac.")) }
        return true
    }

    private func handleButton(type: CGEventType, event: CGEvent) -> Bool {
        guard model.isRemote else { return false }
        let down = (type == .leftMouseDown || type == .rightMouseDown || type == .otherMouseDown)
        let button: Wire.MouseButton
        switch type {
        case .leftMouseDown, .leftMouseUp: button = .left
        case .rightMouseDown, .rightMouseUp: button = .right
        default:
            // Quartz lumps middle and the side buttons together under "other".
            switch event.getIntegerValueField(.mouseEventButtonNumber) {
            case 2: button = .middle
            case 3: button = .x1
            case 4: button = .x2
            default: return true // unknown extra button: swallow, don't forward
            }
        }
        connection.post(.mouseButton, Wire.mouseButtonBody(button, down: down))
        return true
    }

    private func handleScroll(_ event: CGEvent) -> Bool {
        guard model.isRemote else { return false }

        var notchesY = 0.0
        var notchesX = 0.0
        if event.getIntegerValueField(.scrollWheelEventIsContinuous) != 0 {
            // Trackpad: pixel-precise. Accumulate so slow two-finger scrolling
            // still produces steady notches instead of nothing at all.
            scrollRemainderY += Double(event.getIntegerValueField(.scrollWheelEventPointDeltaAxis1))
            scrollRemainderX += Double(event.getIntegerValueField(.scrollWheelEventPointDeltaAxis2))
            notchesY = (scrollRemainderY / settings.pixelsPerNotch).rounded(.towardZero)
            notchesX = (scrollRemainderX / settings.pixelsPerNotch).rounded(.towardZero)
            scrollRemainderY -= notchesY * settings.pixelsPerNotch
            scrollRemainderX -= notchesX * settings.pixelsPerNotch
        } else {
            notchesY = Double(event.getIntegerValueField(.scrollWheelEventDeltaAxis1))
            notchesX = Double(event.getIntegerValueField(.scrollWheelEventDeltaAxis2))
        }

        if settings.invertScroll { notchesY = -notchesY }
        guard notchesX != 0 || notchesY != 0 else { return true }
        connection.post(.mouseWheel, Wire.mouseWheelBody(
            dx: Int16(clamping: Int(notchesX)),
            dy: Int16(clamping: Int(notchesY))
        ))
        return true
    }

    private func handleKey(_ event: CGEvent, down: Bool) -> Bool {
        let keycode = UInt16(event.getIntegerValueField(.keyboardEventKeycode))
        if down, isPanicHotkey(keycode: keycode, flags: event.flags) {
            panic("released with the escape hotkey")
            return true
        }
        guard model.isRemote else {
            if Session.trace { print("  trace KEY 0x\(String(keycode, radix: 16)) down=\(down) DROPPED (not remote)") }
            return false
        }
        guard let scancode = KeyMap.scancode(forVirtualKey: keycode) else {
            if Session.trace { print("  trace KEY 0x\(String(keycode, radix: 16)) has no Windows equivalent") }
            return true // swallow rather than leak to the Mac
        }
        if Session.trace { print("  trace KEY 0x\(String(keycode, radix: 16)) down=\(down) -> scancode 0x\(String(scancode.code, radix: 16))") }
        connection.post(.key, Wire.keyBody(
            scancode: scancode.code, down: down, extended: scancode.extended
        ))
        return true
    }

    private func handleModifier(_ event: CGEvent) -> Bool {
        guard model.isRemote else { return false }
        let keycode = UInt16(event.getIntegerValueField(.keyboardEventKeycode))
        guard let modifier = KeyMap.Modifier(rawValue: keycode) else { return true }

        // flagsChanged carries no up/down, so read it off the resulting flags.
        let down = event.flags.contains(flag(for: modifier))
        let scancode = KeyMap.scancode(for: modifier, mapping: settings.modifiers)
        if Session.trace { print("  trace MOD \(modifier) down=\(down) -> scancode 0x\(String(scancode.code, radix: 16))") }
        connection.post(.key, Wire.keyBody(
            scancode: scancode.code, down: down, extended: scancode.extended
        ))
        return true
    }

    private func flag(for modifier: KeyMap.Modifier) -> CGEventFlags {
        switch modifier {
        case .leftCommand, .rightCommand: return .maskCommand
        case .leftShift, .rightShift: return .maskShift
        case .leftOption, .rightOption: return .maskAlternate
        case .leftControl, .rightControl: return .maskControl
        case .capsLock: return .maskAlphaShift
        }
    }

    /// Ctrl+Option+Cmd+P, swallowed and never forwarded, so there is always a
    /// way back even if the cursor is stranded.
    private func isPanicHotkey(keycode: UInt16, flags: CGEventFlags) -> Bool {
        keycode == 0x23 // P
            && flags.contains(.maskControl)
            && flags.contains(.maskAlternate)
            && flags.contains(.maskCommand)
    }

    // MARK: - Displays

    func refreshDisplays() {
        displays = Displays.all()
        desktop = Displays.union(displays)
    }

    /// True when this display's configured edge is also the edge of the whole
    /// Mac desktop — otherwise there is another Mac display beyond it.
    private func isOuterEdge(of display: Rect) -> Bool {
        let slack = 1.0
        switch settings.capture.edge {
        case .right: return abs(display.maxX - desktop.maxX) < slack
        case .left: return abs(display.x - desktop.x) < slack
        case .bottom: return abs(display.maxY - desktop.maxY) < slack
        case .top: return abs(display.y - desktop.y) < slack
        }
    }
}

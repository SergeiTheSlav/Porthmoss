import CoreGraphics
import Foundation
import PorthmossCore

/// Applies input sent by the PC to this Mac.
///
/// The mirror of the agent's SendInput injector. Events are posted at the HID
/// tap so they reach every application, and each one is stamped with
/// `injectedMarker` so Porthmoss's own event tap can tell them apart from the
/// user's hands — without that the Mac would forward its own injected motion
/// straight back to the PC.
final class MacInjector {
    /// Written into every injected event's user data. Any value would do; this
    /// one is recognisable in a debugger.
    static let injectedMarker: Int64 = 0x504F_5254 // "PORT"

    /// The whole Mac desktop, which incoming normalised coordinates map onto.
    private var desktop: Rect
    /// Where the PC believes this Mac's cursor is, in global coordinates.
    private var cursor = CGPoint.zero

    private var heldButtons: Set<Wire.MouseButton> = []
    private var heldKeys: [(code: UInt16, extended: Bool)] = []
    private var flags: CGEventFlags = []

    init() {
        desktop = Displays.union(Displays.all())
    }

    /// Re-reads the display layout, for a monitor plugged in mid-session.
    func refreshDisplays() {
        desktop = Displays.union(Displays.all())
    }

    // MARK: - Pointer

    /// Takes control: the PC's cursor has crossed onto this Mac.
    func enter(x: UInt16, y: UInt16) {
        moveTo(x: x, y: y)
    }

    func moveTo(x: UInt16, y: UInt16) {
        cursor = point(fromNormalised: x, y)
        // A move with a button held has to be a drag, or nothing being dragged
        // will follow the pointer.
        let type: CGEventType
        if heldButtons.contains(.left) { type = .leftMouseDragged }
        else if heldButtons.contains(.right) { type = .rightMouseDragged }
        else if heldButtons.isEmpty { type = .mouseMoved }
        else { type = .otherMouseDragged }
        post(mouse: type, button: .left)
    }

    func button(_ button: Wire.MouseButton, down: Bool) {
        if down { heldButtons.insert(button) } else { heldButtons.remove(button) }

        let type: CGEventType
        switch button {
        case .left: type = down ? .leftMouseDown : .leftMouseUp
        case .right: type = down ? .rightMouseDown : .rightMouseUp
        default: type = down ? .otherMouseDown : .otherMouseUp
        }
        post(mouse: type, button: cgButton(for: button))
    }

    func wheel(dx: Int16, dy: Int16) {
        guard let event = CGEvent(
            scrollWheelEvent2Source: nil, units: .line,
            wheelCount: 2, wheel1: Int32(dy), wheel2: Int32(dx), wheel3: 0
        ) else { return }
        event.flags = flags
        event.setIntegerValueField(.eventSourceUserData, value: MacInjector.injectedMarker)
        event.post(tap: .cghidEventTap)
    }

    // MARK: - Keyboard

    func key(scancode: UInt16, down: Bool, extended: Bool) {
        // Modifiers change the flag mask that every later event carries, which
        // is how macOS represents them — there is no "shift is down" event
        // separate from the flags on the keystroke itself.
        if let modifier = KeyMap.modifier(forScancode: scancode, extended: extended) {
            let mask = flagMask(for: modifier)
            if down { flags.insert(mask) } else { flags.remove(mask) }
            postFlagsChanged(keycode: modifier.rawValue)
            track(scancode: scancode, extended: extended, down: down)
            return
        }

        guard let keycode = KeyMap.virtualKey(forScancode: scancode, extended: extended),
              let event = CGEvent(keyboardEventSource: nil, virtualKey: CGKeyCode(keycode), keyDown: down)
        else { return }
        event.flags = flags
        event.setIntegerValueField(.eventSourceUserData, value: MacInjector.injectedMarker)
        event.post(tap: .cghidEventTap)
        track(scancode: scancode, extended: extended, down: down)
    }

    /// Releases everything still held. Called when the PC hands control back,
    /// and when the link drops — a modifier stuck down is worse than a lost
    /// keystroke, because every subsequent key does the wrong thing.
    func releaseAll() {
        for held in heldKeys.reversed() {
            key(scancode: held.code, down: false, extended: held.extended)
        }
        heldKeys.removeAll()
        for button in heldButtons {
            self.button(button, down: false)
        }
        heldButtons.removeAll()
        flags = []
    }

    // MARK: - Internals

    private func track(scancode: UInt16, extended: Bool, down: Bool) {
        if down {
            heldKeys.append((scancode, extended))
        } else {
            heldKeys.removeAll { $0.code == scancode && $0.extended == extended }
        }
    }

    private func point(fromNormalised x: UInt16, _ y: UInt16) -> CGPoint {
        CGPoint(
            x: desktop.x + Double(x) / 65535 * max(desktop.width - 1, 1),
            y: desktop.y + Double(y) / 65535 * max(desktop.height - 1, 1)
        )
    }

    private func post(mouse type: CGEventType, button: CGMouseButton) {
        guard let event = CGEvent(
            mouseEventSource: nil, mouseType: type,
            mouseCursorPosition: cursor, mouseButton: button
        ) else { return }
        event.flags = flags
        event.setIntegerValueField(.eventSourceUserData, value: MacInjector.injectedMarker)
        event.post(tap: .cghidEventTap)
    }

    private func postFlagsChanged(keycode: UInt16) {
        guard let event = CGEvent(
            keyboardEventSource: nil, virtualKey: CGKeyCode(keycode), keyDown: true
        ) else { return }
        event.type = .flagsChanged
        event.flags = flags
        event.setIntegerValueField(.eventSourceUserData, value: MacInjector.injectedMarker)
        event.post(tap: .cghidEventTap)
    }

    private func cgButton(for button: Wire.MouseButton) -> CGMouseButton {
        switch button {
        case .left: return .left
        case .right: return .right
        case .middle: return .center
        case .x1, .x2: return .center
        }
    }

    private func flagMask(for modifier: KeyMap.Modifier) -> CGEventFlags {
        switch modifier {
        case .leftCommand, .rightCommand: return .maskCommand
        case .leftShift, .rightShift: return .maskShift
        case .leftOption, .rightOption: return .maskAlternate
        case .leftControl, .rightControl: return .maskControl
        case .capsLock: return .maskAlphaShift
        }
    }
}

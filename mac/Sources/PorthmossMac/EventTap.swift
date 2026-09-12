import CoreGraphics
import Foundation

/// A global CGEventTap that can *swallow* events.
///
/// `NSEvent.addGlobalMonitorForEvents` can observe input but never consume it,
/// which is useless here — while the Mac is driving Windows, its own cursor and
/// keyboard must not react. A head-inserted HID tap is the only API that can do
/// both, and it needs Accessibility *and* Input Monitoring permission.
final class EventTap {
    /// Return true to swallow the event so the Mac never sees it.
    typealias Handler = (CGEventType, CGEvent) -> Bool

    private var machPort: CFMachPort?
    private var runLoopSource: CFRunLoopSource?
    private let handler: Handler

    static let mask: CGEventMask = {
        let types: [CGEventType] = [
            .mouseMoved, .leftMouseDragged, .rightMouseDragged, .otherMouseDragged,
            .leftMouseDown, .leftMouseUp, .rightMouseDown, .rightMouseUp,
            .otherMouseDown, .otherMouseUp, .scrollWheel,
            .keyDown, .keyUp, .flagsChanged,
        ]
        return types.reduce(CGEventMask(0)) { $0 | (1 << CGEventMask($1.rawValue)) }
    }()

    init(handler: @escaping Handler) {
        self.handler = handler
    }

    enum StartError: Error, CustomStringConvertible {
        case permissionDenied

        var description: String {
            """
            Porthmoss could not install its event tap.

            Grant it both permissions in System Settings > Privacy & Security:
              • Accessibility
              • Input Monitoring

            Then run it again. (Both are required: Accessibility to modify
            events, Input Monitoring to see keystrokes at all.)
            """
        }
    }

    func start() throws {
        let refcon = Unmanaged.passUnretained(self).toOpaque()
        guard let port = CGEvent.tapCreate(
            tap: .cghidEventTap,
            place: .headInsertEventTap,
            options: .defaultTap, // not listenOnly: we need to swallow events
            eventsOfInterest: EventTap.mask,
            callback: { _, type, event, refcon in
                guard let refcon else { return Unmanaged.passUnretained(event) }
                let tap = Unmanaged<EventTap>.fromOpaque(refcon).takeUnretainedValue()
                return tap.dispatch(type: type, event: event)
            },
            userInfo: refcon
        ) else {
            throw StartError.permissionDenied
        }

        machPort = port
        runLoopSource = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, port, 0)
        CFRunLoopAddSource(CFRunLoopGetCurrent(), runLoopSource, .commonModes)
        CGEvent.tapEnable(tap: port, enable: true)
    }

    func stop() {
        if let port = machPort { CGEvent.tapEnable(tap: port, enable: false) }
        if let source = runLoopSource {
            CFRunLoopRemoveSource(CFRunLoopGetCurrent(), source, .commonModes)
        }
        machPort = nil
        runLoopSource = nil
    }

    private func dispatch(type: CGEventType, event: CGEvent) -> Unmanaged<CGEvent>? {
        // The system disables a tap whose callback is too slow, and silently
        // stops delivering events until it is switched back on. Missing this is
        // the classic way an event tap "randomly stops working".
        if type == .tapDisabledByTimeout || type == .tapDisabledByUserInput {
            if let port = machPort { CGEvent.tapEnable(tap: port, enable: true) }
            return nil
        }
        return handler(type, event) ? nil : Unmanaged.passUnretained(event)
    }
}

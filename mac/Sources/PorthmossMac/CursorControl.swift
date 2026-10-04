import CoreGraphics
import PorthmossCore

/// Parks and restores the Mac's own cursor while Windows is being driven.
enum CursorControl {
    // Only ever touched from the event tap callback and the session timer,
    // both of which run on the main run loop.
    nonisolated(unsafe) private static var park = CGPoint.zero
    nonisolated(unsafe) private static var hidden = false

    static func capture(parkingAt point: Point) {
        park = CGPoint(x: point.x, y: point.y)
        CGAssociateMouseAndMouseCursorPosition(0)
        CGWarpMouseCursorPosition(park)
        // Hide/show is reference counted, so an unbalanced hide would need two
        // shows to undo.
        if !hidden {
            CGDisplayHideCursor(CGMainDisplayID())
            hidden = true
        }
    }

    /// Snaps the cursor back to the parking spot. Called for every motion
    /// event swallowed while the PC is being driven.
    static func pin() {
        CGWarpMouseCursorPosition(park)
    }

    /// Holds the cursor against a crossing edge that has another Mac display
    /// beyond it, while the push to cross adds up.
    ///
    /// Unlike `capture`, the cursor stays visible and the mouse stays
    /// associated: nothing has been taken over yet, and this may well end
    /// with the pointer passing through to that other display instead.
    static func hold(at point: Point) {
        CGWarpMouseCursorPosition(CGPoint(x: point.x, y: point.y))
    }

    static func release(to point: Point) {
        // Warp before re-associating, or the cursor snaps back to wherever the
        // physical mouse "would" have been.
        CGWarpMouseCursorPosition(CGPoint(x: point.x, y: point.y))
        CGAssociateMouseAndMouseCursorPosition(1)
        if hidden {
            CGDisplayShowCursor(CGMainDisplayID())
            hidden = false
        }
    }

    static var location: Point {
        guard let event = CGEvent(source: nil) else { return Point(x: 0, y: 0) }
        return Point(x: event.location.x, y: event.location.y)
    }
}

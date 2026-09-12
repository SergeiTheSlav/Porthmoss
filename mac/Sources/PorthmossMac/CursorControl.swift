import CoreGraphics
import PorthmossCore

/// Parks and restores the Mac's own cursor while Windows is being driven.
///
/// Swallowing a mouse event in a CGEventTap is *not* enough to stop the
/// cursor. A tap filters what applications receive; the window server updates
/// the pointer position from the HID stream either way, so a swallowed move
/// still slides the arrow across the screen.
/// `CGAssociateMouseAndMouseCursorPosition(false)` is meant to decouple the
/// two and is not reliable on its own.
///
/// So the cursor is actively pinned: parked at the point it crossed over, and
/// warped straight back on every movement we intercept. It cannot drift,
/// whatever the window server believes. `CGWarpMouseCursorPosition` generates
/// no events, so this cannot feed back into the tap.
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

/// The Mac's displays, in Quartz global coordinates (origin top-left, matching
/// the coordinates CGEvent reports — deliberately not AppKit's flipped space).
enum Displays {
    static func all() -> [Rect] {
        var count: UInt32 = 0
        guard CGGetActiveDisplayList(0, nil, &count) == .success, count > 0 else { return [] }
        var ids = [CGDirectDisplayID](repeating: 0, count: Int(count))
        guard CGGetActiveDisplayList(count, &ids, &count) == .success else { return [] }
        return ids.prefix(Int(count)).map { id in
            let bounds = CGDisplayBounds(id)
            return Rect(x: bounds.origin.x, y: bounds.origin.y,
                        width: bounds.size.width, height: bounds.size.height)
        }
    }

    /// The bounding box of the whole Mac desktop.
    static func union(_ rects: [Rect]) -> Rect {
        guard let first = rects.first else { return Rect(x: 0, y: 0, width: 0, height: 0) }
        var minX = first.x, minY = first.y, maxX = first.maxX, maxY = first.maxY
        for rect in rects.dropFirst() {
            minX = min(minX, rect.x); minY = min(minY, rect.y)
            maxX = max(maxX, rect.maxX); maxY = max(maxY, rect.maxY)
        }
        return Rect(x: minX, y: minY, width: maxX - minX, height: maxY - minY)
    }

    static func containing(_ point: Point, in rects: [Rect]) -> Rect? {
        rects.first { point.x >= $0.x && point.x < $0.maxX && point.y >= $0.y && point.y < $0.maxY }
    }
}

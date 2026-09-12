import CoreGraphics
import PorthmossCore

/// Parks and restores the Mac's own cursor while Windows is being driven.
enum CursorControl {
    /// Hands the physical mouse to us: the cursor stops moving and is hidden,
    /// but deltas keep arriving, so pushing further past the edge keeps
    /// producing movement instead of jamming against the screen border.
    static func capture() {
        CGAssociateMouseAndMouseCursorPosition(0)
        CGDisplayHideCursor(CGMainDisplayID())
    }

    static func release(to point: Point) {
        // Warp before re-associating, or the cursor snaps back to where the
        // physical mouse "would" have been.
        CGWarpMouseCursorPosition(CGPoint(x: point.x, y: point.y))
        CGAssociateMouseAndMouseCursorPosition(1)
        CGDisplayShowCursor(CGMainDisplayID())
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

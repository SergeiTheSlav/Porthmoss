import Foundation

public struct Point: Equatable, Sendable {
    public var x: Double, y: Double
    public init(x: Double, y: Double) { self.x = x; self.y = y }
}

public struct Rect: Equatable, Sendable {
    public var x: Double, y: Double, width: Double, height: Double
    public init(x: Double, y: Double, width: Double, height: Double) {
        self.x = x; self.y = y; self.width = width; self.height = height
    }
    public var maxX: Double { x + width }
    public var maxY: Double { y + height }
}

/// Which edge of the Mac's desktop leads to the Windows PC.
public enum ScreenEdge: String, Sendable, CaseIterable {
    case left, right, top, bottom
}

public struct CaptureConfig: Sendable {
    /// The edge the Windows PC sits beyond.
    public var edge: ScreenEdge = .right

    /// How hard the user must push past the edge before control crosses over,
    /// in points. Without this, reaching for a scrollbar or a window's close
    /// button would fling the cursor onto the other machine.
    public var pushThreshold: Double = 14

    /// Accumulated push decays if the user pauses for longer than this, so a
    /// slow drift along the edge never adds up to a crossing.
    public var pushWindow: TimeInterval = 0.4

    /// Scales Mac mouse movement into Windows pixels.
    public var sensitivity: Double = 1.0

    /// How far into the PC's desktop the cursor must travel before pushing
    /// back can hand control home again.
    ///
    /// You arrive pinned against the entry edge, which means the return
    /// gesture is already satisfied the moment you land — a couple of leftward
    /// movements would eject you straight back, and the resulting flapping
    /// looks like the Mac cursor moving on its own. Control only becomes
    /// returnable once you have actually gone somewhere.
    public var returnArmDistance: Double = 64

    public init() {}
}

public enum CaptureAction: Equatable, Sendable {
    case none
    /// Take control: hide the Mac cursor and tell the agent where to appear.
    case enterRemote(x: UInt16, y: UInt16)
    case moveRemote(x: UInt16, y: UInt16)
    /// Hand control back, putting the Mac cursor at this global point.
    case returnToLocal(Point)
}

/// The edge-crossing state machine.
///
/// It is deliberately free of CoreGraphics so the crossing rules — which are
/// most of what makes this feel good or awful — can be tested directly.
public final class CaptureModel {
    public private(set) var isRemote = false
    /// Where the cursor is on the Windows desktop, in its virtual-desktop pixels.
    public private(set) var remoteCursor = Point(x: 0, y: 0)

    public var config: CaptureConfig
    public var screens: RemoteScreens

    /// The Mac display the crossing happens on, in global coordinates.
    public var localDisplay: Rect

    private var push: Double = 0
    private var lastPushAt: TimeInterval = 0
    /// False from the moment control crosses over until the cursor has moved
    /// `returnArmDistance` clear of the entry edge.
    private var returnArmed = false
    /// Where on the edge we left, as a 0...1 fraction, so we come back to the
    /// same place instead of jumping to a corner.
    private var exitFraction: Double = 0.5

    public init(config: CaptureConfig, screens: RemoteScreens, localDisplay: Rect) {
        self.config = config
        self.screens = screens
        self.localDisplay = localDisplay
    }

    /// Feeds one mouse movement in. `cursor` is the Mac's global cursor
    /// position (ignored while remote, where the cursor is parked).
    public func mouseMoved(cursor: Point, delta: Point, now: TimeInterval) -> CaptureAction {
        isRemote ? moveWhileRemote(delta: delta, now: now) : maybeCross(cursor: cursor, delta: delta, now: now)
    }

    /// Forces control back to the Mac — used by the panic hotkey and whenever
    /// the connection drops.
    public func forceReturn() -> CaptureAction? {
        guard isRemote else { return nil }
        isRemote = false
        push = 0
        returnArmed = false
        return .returnToLocal(localPointOnEdge(fraction: exitFraction))
    }

    // MARK: - Local side

    private func maybeCross(cursor: Point, delta: Point, now: TimeInterval) -> CaptureAction {
        let outward = outwardComponent(of: delta)
        guard outward > 0, isAtEdge(cursor) else {
            push = 0
            return .none
        }
        if now - lastPushAt > config.pushWindow { push = 0 }
        push += outward
        lastPushAt = now
        guard push >= config.pushThreshold else { return .none }

        push = 0
        isRemote = true
        returnArmed = false
        exitFraction = edgeFraction(of: cursor)
        remoteCursor = remoteEntryPoint(fraction: exitFraction)
        let (x, y) = normalized(remoteCursor)
        return .enterRemote(x: x, y: y)
    }

    private func isAtEdge(_ cursor: Point) -> Bool {
        let slack = 1.0
        switch config.edge {
        case .right: return cursor.x >= localDisplay.maxX - slack
        case .left: return cursor.x <= localDisplay.x + slack
        case .bottom: return cursor.y >= localDisplay.maxY - slack
        case .top: return cursor.y <= localDisplay.y + slack
        }
    }

    /// How much of this movement pushes *through* the configured edge.
    private func outwardComponent(of delta: Point) -> Double {
        switch config.edge {
        case .right: return delta.x
        case .left: return -delta.x
        case .bottom: return delta.y
        case .top: return -delta.y
        }
    }

    private func edgeFraction(of cursor: Point) -> Double {
        switch config.edge {
        case .left, .right:
            return clamp((cursor.y - localDisplay.y) / max(localDisplay.height, 1), 0, 1)
        case .top, .bottom:
            return clamp((cursor.x - localDisplay.x) / max(localDisplay.width, 1), 0, 1)
        }
    }

    private func localPointOnEdge(fraction: Double) -> Point {
        // Come back one point inside the edge, so the very next movement
        // towards it is a fresh push rather than an instant re-crossing.
        let inset = 2.0
        switch config.edge {
        case .right:
            return Point(x: localDisplay.maxX - inset, y: localDisplay.y + fraction * localDisplay.height)
        case .left:
            return Point(x: localDisplay.x + inset, y: localDisplay.y + fraction * localDisplay.height)
        case .bottom:
            return Point(x: localDisplay.x + fraction * localDisplay.width, y: localDisplay.maxY - inset)
        case .top:
            return Point(x: localDisplay.x + fraction * localDisplay.width, y: localDisplay.y + inset)
        }
    }

    // MARK: - Remote side

    private func moveWhileRemote(delta: Point, now: TimeInterval) -> CaptureAction {
        let virtual = screens.virtualDesktop
        let scaled = Point(x: delta.x * config.sensitivity, y: delta.y * config.sensitivity)

        if !returnArmed, distanceFromEntryEdge() >= config.returnArmDistance {
            returnArmed = true
        }

        // Movement back towards the Mac only counts once the cursor is already
        // pinned against the entry edge of the Windows desktop — and only once
        // it has been somewhere else first.
        let pinned = returnArmed && isPinnedToEntryEdge()
        let inward = -outwardComponent(of: scaled)
        if pinned, inward > 0 {
            if now - lastPushAt > config.pushWindow { push = 0 }
            push += inward
            lastPushAt = now
            if push >= config.pushThreshold {
                push = 0
                isRemote = false
                exitFraction = remoteEdgeFraction()
                return .returnToLocal(localPointOnEdge(fraction: exitFraction))
            }
        } else if inward <= 0 {
            push = 0
        }

        remoteCursor.x = clamp(remoteCursor.x + scaled.x, Double(virtual.left), Double(virtual.right - 1))
        remoteCursor.y = clamp(remoteCursor.y + scaled.y, Double(virtual.top), Double(virtual.bottom - 1))
        let (x, y) = normalized(remoteCursor)
        return .moveRemote(x: x, y: y)
    }

    /// The Windows desktop edge the cursor arrived through — crossing the Mac's
    /// right edge means entering Windows from its left.
    private func isPinnedToEntryEdge() -> Bool {
        let virtual = screens.virtualDesktop
        let slack = 1.0
        switch config.edge {
        case .right: return remoteCursor.x <= Double(virtual.left) + slack
        case .left: return remoteCursor.x >= Double(virtual.right - 1) - slack
        case .bottom: return remoteCursor.y <= Double(virtual.top) + slack
        case .top: return remoteCursor.y >= Double(virtual.bottom - 1) - slack
        }
    }

    /// How far the cursor has travelled into the PC's desktop, measured from
    /// the edge it arrived through.
    private func distanceFromEntryEdge() -> Double {
        let virtual = screens.virtualDesktop
        switch config.edge {
        case .right: return remoteCursor.x - Double(virtual.left)
        case .left: return Double(virtual.right - 1) - remoteCursor.x
        case .bottom: return remoteCursor.y - Double(virtual.top)
        case .top: return Double(virtual.bottom - 1) - remoteCursor.y
        }
    }

    private func remoteEntryPoint(fraction: Double) -> Point {
        let virtual = screens.virtualDesktop
        switch config.edge {
        case .right:
            return Point(x: Double(virtual.left), y: Double(virtual.top) + fraction * Double(virtual.height))
        case .left:
            return Point(x: Double(virtual.right - 1), y: Double(virtual.top) + fraction * Double(virtual.height))
        case .bottom:
            return Point(x: Double(virtual.left) + fraction * Double(virtual.width), y: Double(virtual.top))
        case .top:
            return Point(x: Double(virtual.left) + fraction * Double(virtual.width), y: Double(virtual.bottom - 1))
        }
    }

    private func remoteEdgeFraction() -> Double {
        let virtual = screens.virtualDesktop
        switch config.edge {
        case .left, .right:
            return clamp((remoteCursor.y - Double(virtual.top)) / Double(max(virtual.height, 1)), 0, 1)
        case .top, .bottom:
            return clamp((remoteCursor.x - Double(virtual.left)) / Double(max(virtual.width, 1)), 0, 1)
        }
    }

    /// Converts virtual-desktop pixels to the 0...65535 space SendInput wants.
    private func normalized(_ point: Point) -> (UInt16, UInt16) {
        let virtual = screens.virtualDesktop
        let nx = 65535 * (point.x - Double(virtual.left)) / Double(max(virtual.width - 1, 1))
        let ny = 65535 * (point.y - Double(virtual.top)) / Double(max(virtual.height - 1, 1))
        return (UInt16(clamp(nx.rounded(), 0, 65535)), UInt16(clamp(ny.rounded(), 0, 65535)))
    }
}

func clamp(_ value: Double, _ low: Double, _ high: Double) -> Double {
    min(max(value, low), high)
}

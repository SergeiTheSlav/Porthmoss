import AppKit
import CoreGraphics
import PorthmossCore

/// One of the Mac's displays.
///
/// In Quartz global coordinates, origin top-left, matching what CGEvent
/// reports, not AppKit's flipped space.
struct Display: Identifiable, Equatable, Sendable {
    /// Stable enough to write into a settings file and still mean the same
    /// monitor after a reboot or a replug. See `Displays.identifier`.
    let id: String
    /// What System Settings calls it, so the picker says "Studio Display"
    /// rather than "Display 2".
    let name: String
    let frame: Rect
    let isMain: Bool
}

/// The Mac's displays, and the rules about which of their edges can lead to
/// the Windows PC.
enum Displays {
    static func all() -> [Display] {
        var count: UInt32 = 0
        guard CGGetActiveDisplayList(0, nil, &count) == .success, count > 0 else { return [] }
        var ids = [CGDirectDisplayID](repeating: 0, count: Int(count))
        guard CGGetActiveDisplayList(count, &ids, &count) == .success else { return [] }

        let names = localizedNames()
        var seen: Set<String> = []
        return ids.prefix(Int(count)).enumerated().map { index, id in
            // Two identical monitors of a model that reports no serial number
            // produce the same identifier. Fall back to the port they are
            // plugged into, which at least distinguishes them while they stay
            // plugged into the same ports.
            var identifier = Displays.identifier(of: id)
            if !seen.insert(identifier).inserted {
                identifier += "-\(CGDisplayUnitNumber(id))"
            }
            let bounds = CGDisplayBounds(id)
            return Display(
                id: identifier,
                name: names[id] ?? fallbackName(of: id, index: index),
                frame: Rect(x: bounds.origin.x, y: bounds.origin.y,
                            width: bounds.size.width, height: bounds.size.height),
                isMain: CGDisplayIsMain(id) != 0
            )
        }
    }

    /// An identifier built from what the monitor says about itself, rather
    /// than from its `CGDirectDisplayID`, which is handed out afresh on every
    /// boot and would make a saved choice point at a different screen.
    private static func identifier(of id: CGDirectDisplayID) -> String {
        "\(CGDisplayVendorNumber(id))-\(CGDisplayModelNumber(id))-\(CGDisplaySerialNumber(id))"
    }

    /// `NSScreen.localizedName` is the only place the user-facing name lives,
    /// and NSScreen is keyed by display ID in its device description.
    private static func localizedNames() -> [CGDirectDisplayID: String] {
        var names: [CGDirectDisplayID: String] = [:]
        for screen in NSScreen.screens {
            let key = NSDeviceDescriptionKey("NSScreenNumber")
            guard let number = screen.deviceDescription[key] as? NSNumber else { continue }
            names[CGDirectDisplayID(number.uint32Value)] = screen.localizedName
        }
        return names
    }

    private static func fallbackName(of id: CGDirectDisplayID, index: Int) -> String {
        CGDisplayIsBuiltin(id) != 0 ? "Built-in display" : "Display \(index + 1)"
    }

    static func containing(_ point: Point, in displays: [Display]) -> Display? {
        displays.first { $0.frame.contains(point) }
    }

    /// True when nothing of the Mac's own is beyond this display's `edge`, so
    /// the pointer leaving it has nowhere to go but the PC.
    static func isFree(_ display: Display, edge: ScreenEdge, among displays: [Display]) -> Bool {
        !displays.contains { other in
            guard other.id != display.id else { return false }
            let mine = display.frame, theirs = other.frame
            switch edge {
            case .right:
                return theirs.maxX > mine.maxX && overlap(mine.y, mine.maxY, theirs.y, theirs.maxY)
            case .left:
                return theirs.x < mine.x && overlap(mine.y, mine.maxY, theirs.y, theirs.maxY)
            case .bottom:
                return theirs.maxY > mine.maxY && overlap(mine.x, mine.maxX, theirs.x, theirs.maxX)
            case .top:
                return theirs.y < mine.y && overlap(mine.x, mine.maxX, theirs.x, theirs.maxX)
            }
        }
    }

    /// Whether two one-dimensional spans share any length at all. Displays
    /// that merely touch, which is how every tidy arrangement is set up,    /// share none.
    private static func overlap(_ a0: Double, _ a1: Double, _ b0: Double, _ b1: Double) -> Bool {
        min(a1, b1) - max(a0, b0) > 0
    }
}

import CoreGraphics
import Foundation

// Reproduction for "the Mac cursor moves while the PC is being driven".
//
// Horizontal drift is invisible at the right edge — the cursor is already
// clamped there. Vertical drift is not, so that is what this measures: push
// through the edge to cross over, then move only up and down and see whether
// the Mac cursor follows.

func cursor() -> CGPoint { CGEvent(source: nil)!.location }

let home = cursor()

// Push at the outer edge of the *whole* desktop, not of the main display.
// With a second monitor attached, the main display's right edge is an internal
// boundary between two Mac displays, and Porthmoss quite rightly refuses to
// cross to the PC there — which made this probe look like a drift failure when
// it was really testing the wrong edge.
func displays() -> [CGRect] {
    var count: UInt32 = 0
    guard CGGetActiveDisplayList(0, nil, &count) == .success, count > 0 else { return [] }
    var ids = [CGDirectDisplayID](repeating: 0, count: Int(count))
    guard CGGetActiveDisplayList(count, &ids, &count) == .success else { return [] }
    return ids.prefix(Int(count)).map(CGDisplayBounds)
}

let all = displays()
guard let rightmost = all.max(by: { $0.maxX < $1.maxX }) else {
    print("no displays"); exit(2)
}
let screen = rightmost
let edgeX = screen.maxX - 1
var y = screen.midY

func post(dx: Int64, dy: Int64, at point: CGPoint) {
    let source = CGEventSource(stateID: .hidSystemState)
    guard let event = CGEvent(mouseEventSource: source, mouseType: .mouseMoved,
                              mouseCursorPosition: point, mouseButton: .left) else { return }
    event.setIntegerValueField(.mouseEventDeltaX, value: dx)
    event.setIntegerValueField(.mouseEventDeltaY, value: dy)
    event.post(tap: .cghidEventTap)
    usleep(16_000)
}

// Park on the edge, then push through it.
CGWarpMouseCursorPosition(CGPoint(x: edgeX, y: y))
usleep(300_000)
for _ in 0 ..< 8 { post(dx: 8, dy: 0, at: CGPoint(x: edgeX, y: y)) }
usleep(200_000)

let afterCrossing = cursor()
print(String(format: "after crossing: (%.0f, %.0f)", afterCrossing.x, afterCrossing.y))

// Now move only vertically, as if using the PC.
for step in 1 ... 25 {
    y = screen.midY - Double(step) * 6
    post(dx: 0, dy: -6, at: CGPoint(x: edgeX, y: y))
}
usleep(200_000)

let afterMoving = cursor()
print(String(format: "after 25 vertical moves: (%.0f, %.0f)", afterMoving.x, afterMoving.y))

let drift = abs(afterMoving.y - afterCrossing.y)
print(String(format: "vertical drift: %.0f px", drift))
print(drift < 3 ? "PASS — the Mac cursor stayed put" : "FAIL — the Mac cursor followed the mouse")

CGWarpMouseCursorPosition(home)
CGAssociateMouseAndMouseCursorPosition(1)
exit(drift < 3 ? 0 : 1)

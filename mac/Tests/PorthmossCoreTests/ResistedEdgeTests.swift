import Testing
@testable import PorthmossCore

/// Crossing at an edge that has another of the Mac's own displays beyond it.
///
/// This is what makes it possible to put the PC past the *inner* edge of two
/// side-by-side screens. It needs its own rules, because the window server has
/// already moved the pointer onto the neighbouring display by the time the
/// second movement arrives — so without holding the cursor still, the push can
/// never add up and the crossing simply never fires.
///
/// The cost is that the ordinary way of reaching that neighbour runs into
/// resistance, so pausing mid-push has to let the pointer through. Both halves
/// of that bargain are pinned down here.
private let macDisplay = Rect(x: 0, y: 0, width: 1512, height: 982)
private let windowsScreens = RemoteScreens(
    virtualDesktop: RemoteMonitor(left: 0, top: 0, width: 2560, height: 1440),
    monitors: [RemoteMonitor(left: 0, top: 0, width: 2560, height: 1440, isPrimary: true)]
)

private func makeResistingModel() -> CaptureModel {
    var config = CaptureConfig()
    config.edge = .right
    config.resistAtEdge = true
    return CaptureModel(config: config, screens: windowsScreens, localDisplay: macDisplay)
}

@Suite("Crossing at an edge with another screen beyond it")
struct ResistedEdgeTests {
    @Test("The cursor is held at the edge while the push adds up")
    func holdsWhileAccumulating() {
        let model = makeResistingModel()
        // The pointer is reported past the edge, because the window server has
        // already moved it onto the display beyond. That must not end the push.
        let action = model.mouseMoved(
            cursor: Point(x: macDisplay.maxX + 4, y: 491), delta: Point(x: 4, y: 0), now: 0
        )
        guard case let .holdAtEdge(point) = action else {
            Issue.record("expected the cursor to be held; got \(action)")
            return
        }
        #expect(model.isHoldingAtEdge)
        // Held just inside the edge, at the height it was pushed at.
        #expect(abs(point.x - (macDisplay.maxX - 2)) < 0.001)
        #expect(abs(point.y - 491) < 1)
        #expect(!model.isRemote)
    }

    @Test("A sustained push still crosses over")
    func sustainedPushCrosses() {
        let model = makeResistingModel()
        var crossed: CaptureAction = .none
        for i in 0 ..< 10 {
            let action = model.mouseMoved(
                cursor: Point(x: macDisplay.maxX + 4, y: 491),
                delta: Point(x: 5, y: 0),
                now: Double(i) * 0.02
            )
            if case .enterRemote = action { crossed = action; break }
        }
        guard case let .enterRemote(x, _) = crossed else {
            Issue.record("never crossed; got \(crossed)")
            return
        }
        #expect(x == 0)
        #expect(model.isRemote)
        // Nothing is being held any more: the cursor is parked and hidden.
        #expect(!model.isHoldingAtEdge)
    }

    @Test("Pausing mid-push lets the pointer through to the other screen")
    func pausingGivesUpTheHold() {
        let model = makeResistingModel()
        let atEdge = Point(x: macDisplay.maxX + 4, y: 491)

        // Start pushing…
        #expect(model.mouseMoved(cursor: atEdge, delta: Point(x: 6, y: 0), now: 0) != .none)
        #expect(model.isHoldingAtEdge)

        // …then stop. The next movement arrives after the push window, which
        // is the gesture for "I meant the screen next door".
        #expect(model.mouseMoved(cursor: atEdge, delta: Point(x: 6, y: 0), now: 1.0) == .none)
        #expect(!model.isHoldingAtEdge)

        // From here the pointer must be left alone, however hard it is pushed,
        // or the other screen would be unreachable.
        for i in 0 ..< 20 {
            let action = model.mouseMoved(
                cursor: atEdge, delta: Point(x: 10, y: 0), now: 1.02 + Double(i) * 0.016
            )
            #expect(action == .none)
        }
        #expect(!model.isRemote)
    }

    @Test("Coming away from the edge re-arms the crossing")
    func movingAwayReArms() {
        let model = makeResistingModel()
        let atEdge = Point(x: macDisplay.maxX + 4, y: 491)
        _ = model.mouseMoved(cursor: atEdge, delta: Point(x: 6, y: 0), now: 0)
        _ = model.mouseMoved(cursor: atEdge, delta: Point(x: 6, y: 0), now: 1.0) // gives up

        // Back into the middle of the screen, then at the edge again. Having
        // given up once must not disable the crossing for the rest of the
        // session — the pointer only passed through to look at something.
        _ = model.mouseMoved(cursor: Point(x: 700, y: 491), delta: Point(x: -40, y: 0), now: 2.0)

        var crossed = false
        for i in 0 ..< 10 {
            let action = model.mouseMoved(
                cursor: atEdge, delta: Point(x: 5, y: 0), now: 3.0 + Double(i) * 0.02
            )
            if case .enterRemote = action { crossed = true; break }
        }
        #expect(crossed, "a fresh push after coming away from the edge must cross")
    }

    @Test("Sliding along the edge is not a push and holds nothing")
    func slidingAlongTheEdgeDoesNotHold() {
        let model = makeResistingModel()
        // Dragging a window title bar along the top of the other screen, or
        // reaching for something at the edge: vertical movement only.
        for i in 0 ..< 20 {
            let action = model.mouseMoved(
                cursor: Point(x: macDisplay.maxX, y: 200 + Double(i) * 10),
                delta: Point(x: 0, y: 10),
                now: Double(i) * 0.016
            )
            #expect(action == .none)
        }
        #expect(!model.isHoldingAtEdge)
        #expect(!model.isRemote)
    }

    @Test("Without resistance the model never asks for a hold")
    func resistanceIsOptIn() {
        // The single-display case, and every outer edge: nothing is in the way,
        // so the cursor must never be touched before control actually crosses.
        var config = CaptureConfig()
        config.edge = .right
        let model = CaptureModel(config: config, screens: windowsScreens, localDisplay: macDisplay)
        for i in 0 ..< 2 {
            let action = model.mouseMoved(
                cursor: Point(x: macDisplay.maxX, y: 491), delta: Point(x: 4, y: 0),
                now: Double(i) * 0.02
            )
            #expect(action == .none)
        }
        #expect(!model.isHoldingAtEdge)
    }
}

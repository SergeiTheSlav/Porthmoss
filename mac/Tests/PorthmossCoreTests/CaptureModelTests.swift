import Testing
@testable import PorthmossCore

private let macDisplay = Rect(x: 0, y: 0, width: 1512, height: 982)
private let windowsScreens = RemoteScreens(
    virtualDesktop: RemoteMonitor(left: 0, top: 0, width: 2560, height: 1440),
    monitors: [RemoteMonitor(left: 0, top: 0, width: 2560, height: 1440, isPrimary: true)]
)

private func makeModel(edge: ScreenEdge = .right) -> CaptureModel {
    var config = CaptureConfig()
    config.edge = edge
    return CaptureModel(config: config, screens: windowsScreens, localDisplay: macDisplay)
}

/// Drives the model until it crosses, failing the test if it never does.
private func crossOver(_ model: CaptureModel, sourceLocation: SourceLocation = #_sourceLocation) {
    for i in 0 ..< 10 {
        _ = model.mouseMoved(
            cursor: Point(x: macDisplay.maxX, y: 491), delta: Point(x: 5, y: 0), now: Double(i) * 0.02
        )
        if model.isRemote { return }
    }
    Issue.record("setup: failed to cross over", sourceLocation: sourceLocation)
}

@Suite("Edge crossing")
struct CaptureModelTests {
    @Test("Brushing the edge does not hand over control")
    func brushingDoesNotCross() {
        let model = makeModel()
        // Reaching for a scrollbar: at the edge, but nowhere near the push
        // threshold. This is the failure mode that makes naive edge detection
        // unusable, so it is the first thing worth pinning down.
        for i in 0 ..< 5 {
            let action = model.mouseMoved(
                cursor: Point(x: macDisplay.maxX, y: 400),
                delta: Point(x: 2, y: 0),
                now: Double(i) * 0.02
            )
            #expect(action == .none)
        }
        #expect(!model.isRemote)
    }

    @Test("A sustained push crosses over at the matching height")
    func sustainedPushCrosses() {
        let model = makeModel()
        var crossed: CaptureAction = .none
        for i in 0 ..< 10 {
            let action = model.mouseMoved(
                cursor: Point(x: macDisplay.maxX, y: 491), // vertically centred
                delta: Point(x: 5, y: 0),
                now: Double(i) * 0.02
            )
            if case .enterRemote = action { crossed = action; break }
        }
        guard case let .enterRemote(x, y) = crossed else {
            Issue.record("never crossed; got \(crossed)")
            return
        }
        #expect(model.isRemote)
        // Crossing the Mac's right edge means arriving at the Windows left edge.
        #expect(x == 0)
        // Halfway down the Mac display should be halfway down the Windows one.
        #expect(abs(Double(y) - 32767) < 400)
    }

    @Test("Push decays, so a slow drift never adds up to a crossing")
    func pausingResetsPush() {
        let model = makeModel()
        let atEdge = Point(x: macDisplay.maxX, y: 400)
        _ = model.mouseMoved(cursor: atEdge, delta: Point(x: 10, y: 0), now: 0)
        let action = model.mouseMoved(cursor: atEdge, delta: Point(x: 10, y: 0), now: 1.0)
        #expect(action == .none)
        #expect(!model.isRemote)
    }

    @Test("The remote cursor stays inside the Windows desktop")
    func cursorIsClamped() {
        let model = makeModel()
        crossOver(model)
        for _ in 0 ..< 200 {
            _ = model.mouseMoved(cursor: Point(x: 0, y: 0), delta: Point(x: 50, y: 50), now: 0)
        }
        #expect(model.remoteCursor.x == 2559)
        #expect(model.remoteCursor.y == 1439)
    }

    @Test("Pushing back at the far edge returns control to the Mac")
    func pushingBackReturns() {
        let model = makeModel()
        crossOver(model)
        _ = model.mouseMoved(cursor: Point(x: 0, y: 0), delta: Point(x: 500, y: 0), now: 1.0)
        // Walk back towards the Mac. Control returns partway through this loop,
        // once the cursor has been pinned against the Windows left edge long
        // enough to clear the push threshold.
        var returned: CaptureAction = .none
        for i in 0 ..< 200 {
            let action = model.mouseMoved(
                cursor: Point(x: 0, y: 0), delta: Point(x: -10, y: 0), now: 1.0 + Double(i) * 0.02
            )
            if case .returnToLocal = action { returned = action; break }
        }
        guard case let .returnToLocal(point) = returned else {
            Issue.record("control never came back; got \(returned)")
            return
        }
        #expect(!model.isRemote)
        #expect(abs(point.x - (macDisplay.maxX - 2)) < 0.001)
    }

    @Test("Moving left mid-screen is movement, not a request to come back")
    func midScreenMovementDoesNotReturn() {
        let model = makeModel()
        crossOver(model)
        _ = model.mouseMoved(cursor: Point(x: 0, y: 0), delta: Point(x: 1200, y: 0), now: 1.0)
        for i in 0 ..< 20 {
            let action = model.mouseMoved(
                cursor: Point(x: 0, y: 0), delta: Point(x: -5, y: 0), now: 1.0 + Double(i) * 0.02
            )
            if case .returnToLocal = action {
                Issue.record("returned while mid-screen")
                return
            }
        }
        #expect(model.isRemote)
    }

    @Test("The panic release always hands control back, and is idempotent")
    func forceReturnReleases() {
        let model = makeModel()
        crossOver(model)
        guard case .returnToLocal? = model.forceReturn() else {
            Issue.record("panic release should hand control back")
            return
        }
        #expect(!model.isRemote)
        #expect(model.forceReturn() == nil)
    }

    @Test("Crossing the left edge arrives on the Windows right edge")
    func leftEdgeEntersFromTheRight() {
        let model = makeModel(edge: .left)
        var crossed: CaptureAction = .none
        for i in 0 ..< 10 {
            let action = model.mouseMoved(
                cursor: Point(x: 0, y: 491), delta: Point(x: -5, y: 0), now: Double(i) * 0.02
            )
            if case .enterRemote = action { crossed = action; break }
        }
        guard case let .enterRemote(x, _) = crossed else {
            Issue.record("never crossed; got \(crossed)")
            return
        }
        #expect(x == 65535)
    }
}

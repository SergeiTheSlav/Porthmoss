import Foundation
import Testing
@testable import PorthmossCore

@Suite("Wire protocol")
struct ProtocolTests {
    @Test("Frames round-trip and decode back to back")
    func frameRoundTrip() throws {
        var buffer = Data()
        buffer.append(try Wire.frame(.mouseMove, Wire.mouseMoveBody(x: 1234, y: 5678)))
        buffer.append(try Wire.frame(.leave))

        let first = try #require(try Wire.nextFrame(from: &buffer))
        #expect(first.type == Wire.MessageType.mouseMove.rawValue)
        #expect(first.body == [0x04, 0xD2, 0x16, 0x2E])

        let second = try #require(try Wire.nextFrame(from: &buffer))
        #expect(second.type == Wire.MessageType.leave.rawValue)
        #expect(second.body.isEmpty)
        #expect(buffer.isEmpty)
    }

    @Test("A partially received frame waits for the rest")
    func partialFrame() throws {
        let complete = try Wire.frame(.ping, Wire.pingBody(7))
        var buffer = complete.dropLast()
        #expect(try Wire.nextFrame(from: &buffer) == nil)

        buffer.append(complete.last!)
        let frame = try #require(try Wire.nextFrame(from: &buffer))
        #expect(try Wire.decodeU64(frame.body) == 7)
    }

    @Test("Scrolling down stays negative on the wire")
    func negativeWheel() {
        // -3 must arrive signed, not as 65533 notches of scroll.
        #expect(Wire.mouseWheelBody(dx: 0, dy: -3) == [0x00, 0x00, 0xFF, 0xFD])
    }

    @Test("Screen layout decodes exactly as the agent encodes it")
    func decodeScreenInfo() throws {
        // Two monitors side by side, the left one primary.
        var body: [UInt8] = []
        for value in [Int32(0), 0, 3840, 1080] { body.appendBigEndianInt32(value) }
        body.append(2)
        for value in [Int32(0), 0, 2560, 1080] { body.appendBigEndianInt32(value) }
        body.append(1)
        for value in [Int32(2560), 0, 1280, 1024] { body.appendBigEndianInt32(value) }
        body.append(0)

        let screens = try RemoteScreens.decode(body)
        #expect(screens.virtualDesktop.width == 3840)
        #expect(screens.monitors.count == 2)
        #expect(screens.monitors[0].isPrimary)
        #expect(screens.monitors[1].left == 2560)
    }

    @Test("A layout claiming more monitors than it carries is rejected")
    func truncatedScreenInfo() {
        var body: [UInt8] = []
        for value in [Int32(0), 0, 1920, 1080] { body.appendBigEndianInt32(value) }
        body.append(3)
        for value in [Int32(0), 0, 1920, 1080] { body.appendBigEndianInt32(value) }
        body.append(1)

        #expect(throws: WireError.truncated) { try RemoteScreens.decode(body) }
    }
}

@Suite("Key mapping")
struct KeyMapTests {
    @Test("Cmd becomes Ctrl so Mac shortcuts keep working")
    func commandMapsToControl() {
        let mapping = ModifierMapping.default
        #expect(KeyMap.scancode(for: .leftCommand, mapping: mapping) == Scancode(0x1D))
        #expect(KeyMap.scancode(for: .rightCommand, mapping: mapping) == Scancode(0x1D, extended: true))
        // Mac Control then takes over the Windows key.
        #expect(KeyMap.scancode(for: .leftControl, mapping: mapping) == Scancode(0x5B, extended: true))
        // ...which makes Cmd+C into Ctrl+C.
        #expect(KeyMap.scancode(forVirtualKey: 0x08) == Scancode(0x2E))
    }

    @Test("Arrow keys carry the E0 prefix Windows expects")
    func arrowsAreExtended() {
        for keycode in [UInt16(0x7B), 0x7C, 0x7D, 0x7E] {
            #expect(KeyMap.scancode(forVirtualKey: keycode)?.extended == true)
        }
        #expect(KeyMap.scancode(forVirtualKey: 0x3F) == nil, "Fn has no Windows equivalent")
    }
}

private extension Array where Element == UInt8 {
    mutating func appendBigEndianInt32(_ value: Int32) {
        appendBigEndian(UInt32(bitPattern: value))
    }
}

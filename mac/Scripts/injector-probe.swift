import CoreGraphics
import Foundation

// Drives the injector the way the PC would, and checks the Mac responded.
// Moves the cursor briefly and puts it back.
func cursor() -> CGPoint { CGEvent(source: nil)!.location }
let home = cursor()
let injector = MacInjector()
nonisolated(unsafe) var failures = 0

func check(_ name: String, _ ok: Bool, _ detail: String = "") {
    print(ok ? "  PASS  \(name)" : "  FAIL  \(name) \(detail)")
    if !ok { failures += 1 }
}

let desktop = Displays.union(Displays.all())

injector.enter(x: 16383, y: 16383)
usleep(250_000)
let quarter = cursor()
let wantQuarter = CGPoint(x: desktop.x + desktop.width * 0.25, y: desktop.y + desktop.height * 0.25)
check("enter moves the cursor to the mapped point",
      hypot(quarter.x - wantQuarter.x, quarter.y - wantQuarter.y) < 4,
      String(format: "got (%.0f,%.0f) want (%.0f,%.0f)", quarter.x, quarter.y, wantQuarter.x, wantQuarter.y))

injector.moveTo(x: 49151, y: 32767)
usleep(250_000)
let later = cursor()
let wantLater = CGPoint(x: desktop.x + desktop.width * 0.75, y: desktop.y + desktop.height * 0.5)
check("moveTo tracks further movement",
      hypot(later.x - wantLater.x, later.y - wantLater.y) < 4,
      String(format: "got (%.0f,%.0f) want (%.0f,%.0f)", later.x, later.y, wantLater.x, wantLater.y))

check("a Windows scancode maps back to a Mac key",
      KeyMap.virtualKey(forScancode: 0x1e, extended: false) == 0x00)
check("the PC's Ctrl becomes Command",
      KeyMap.modifier(forScancode: 0x1d, extended: false) == .leftCommand)

// Park in the top-left corner first. This presses a real button and a real
// modifier into the live system, and doing that wherever the cursor happens to
// be has already flipped a setting in a window that was underneath it.
injector.moveTo(x: 0, y: 0)
usleep(120_000)
injector.button(.left, down: true)
injector.key(scancode: 0x1d, down: true, extended: false)
injector.releaseAll()
usleep(150_000)
check("releaseAll leaves no modifier stuck",
      !CGEvent(source: nil)!.flags.contains(.maskCommand))

CGWarpMouseCursorPosition(home)
print(failures == 0 ? "\nINJECTOR WORKS" : "\n\(failures) FAILED")
exit(failures == 0 ? 0 : 1)

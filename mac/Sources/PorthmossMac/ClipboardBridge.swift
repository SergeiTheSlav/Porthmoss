import AppKit
import Foundation
import PorthmossCore

/// Keeps the Mac's clipboard and the PC's in step.
///
/// The hard part is not copying text, it is not echoing: writing what the PC
/// sent changes the local pasteboard, the poller notices, and without care it
/// sends it straight back and the two bounce it between them forever.
///
/// AppKit has no notification for pasteboard changes — `changeCount` is the
/// supported way to find out, and it has to be polled.
final class ClipboardBridge {
    /// A clipboard is not a file transfer. A 10 MB paste has no business being
    /// mirrored across a link that exists to carry keystrokes.
    static let maxText = 256 * 1024

    private let pasteboard = NSPasteboard.general
    private let send: (String) -> Void

    private var timer: Timer?
    private var lastChangeCount: Int
    private var lastText: String?

    init(send: @escaping (String) -> Void) {
        self.send = send
        lastChangeCount = pasteboard.changeCount
    }

    func start() {
        timer = Timer.scheduledTimer(withTimeInterval: 0.4, repeats: true) { [weak self] _ in
            self?.poll()
        }
    }

    func stop() {
        timer?.invalidate()
        timer = nil
    }

    /// Puts text from the PC on this Mac's pasteboard.
    func applyRemote(_ text: String) {
        lastText = text
        pasteboard.clearContents()
        pasteboard.setString(text, forType: .string)
        // Read the count back after writing, so our own write is not mistaken
        // for the user copying something.
        lastChangeCount = pasteboard.changeCount
    }

    private func poll() {
        let count = pasteboard.changeCount
        guard count != lastChangeCount else { return }
        lastChangeCount = count

        // No string means the pasteboard holds something that is not text,
        // which is not a reason to wipe the PC's.
        guard let text = pasteboard.string(forType: .string), !text.isEmpty else { return }
        guard text != lastText else { return }
        guard text.utf8.count <= ClipboardBridge.maxText else { return }

        lastText = text
        send(text)
    }
}

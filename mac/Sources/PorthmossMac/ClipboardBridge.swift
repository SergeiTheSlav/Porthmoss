import AppKit
import Foundation
import PorthmossCore

/// Keeps the Mac's clipboard and the PC's in step.
final class ClipboardBridge {
    /// A clipboard is not a file transfer. A 10 MB paste has no business being
    /// mirrored across a link that exists to carry keystrokes.
    static let maxText = 256 * 1024

    /// Copying a whole folder's worth of files and pressing paste should fail
    /// at once rather than quietly starting a thousand transfers.
    static let maxFiles = 64

    private let pasteboard = NSPasteboard.general
    private let send: (String) -> Void
    private let sendFiles: ([URL]) -> Void

    private var timer: Timer?
    private var lastChangeCount: Int
    private var lastText: String?
    private var lastPaths: [URL] = []

    init(send: @escaping (String) -> Void, sendFiles: @escaping ([URL]) -> Void) {
        self.send = send
        self.sendFiles = sendFiles
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

    /// Puts files from the PC on this Mac's pasteboard, so the next paste
    /// produces them.
    func applyRemoteFiles(_ urls: [URL]) {
        lastPaths = urls
        pasteboard.clearContents()
        pasteboard.writeObjects(urls as [NSURL])
        lastChangeCount = pasteboard.changeCount
    }

    private func pasteboardFiles() -> [URL] {
        let options: [NSPasteboard.ReadingOptionKey: Any] = [
            .init(rawValue: NSPasteboard.ReadingOptionKey.urlReadingFileURLsOnly.rawValue): true,
        ]
        let found = pasteboard.readObjects(forClasses: [NSURL.self], options: options)
        return (found as? [URL]) ?? []
    }

    /// Set PORTHMOSS_TRACE=1 to see what the pasteboard poller decides.
    private static let trace = ProcessInfo.processInfo.environment["PORTHMOSS_TRACE"] == "1"

    private func poll() {
        let count = pasteboard.changeCount
        guard count != lastChangeCount else { return }
        lastChangeCount = count
        if ClipboardBridge.trace {
            print("  trace CLIPBOARD change \(count), files=\(pasteboardFiles().count)")
        }

        // Files first: copying files in Finder also leaves a text form on the
        // pasteboard, and sending that instead would be the wrong thing.
        let files = pasteboardFiles()
        if !files.isEmpty {
            guard files != lastPaths else { return }
            lastPaths = files
            guard files.count <= ClipboardBridge.maxFiles else { return }
            sendFiles(files)
            return
        }

        // No string means the pasteboard holds something that is not text,
        // which is not a reason to wipe the PC's.
        guard let text = pasteboard.string(forType: .string), !text.isEmpty else { return }
        guard text != lastText else { return }
        guard text.utf8.count <= ClipboardBridge.maxText else { return }

        lastText = text
        send(text)
    }
}

import AppKit
import Foundation
import PorthmossCore

/// Sends files to the PC, and receives the ones it sends back.
enum FileTransfer {
    /// Matches the agent. A convenience for dragging a document across, not a
    /// backup tool: an accidental 4 GB drag should fail at once rather than
    /// after ten minutes.
    static let maxFileSize: UInt64 = 512 << 20
    static let chunkSize = 256 * 1024

    /// Where files dragged from the PC land. Downloads is where a user already
    /// looks for something that arrived from elsewhere.
    static var dropDirectory: URL {
        FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Porthmoss")
    }
}

// MARK: - Sending

extension FileTransfer {
    enum SendError: LocalizedError {
        case tooLarge(name: String, bytes: UInt64)
        case unreadable(name: String)

        var errorDescription: String? {
            switch self {
            case let .tooLarge(name, bytes):
                let limit = ByteCountFormatter.string(fromByteCount: Int64(maxFileSize), countStyle: .file)
                let actual = ByteCountFormatter.string(fromByteCount: Int64(bytes), countStyle: .file)
                return "\(name) is \(actual); the limit is \(limit)."
            case let .unreadable(name):
                return "Could not read \(name)."
            }
        }
    }

    /// Streams one file as BEGIN, CHUNK…, END.
    ///
    /// Read in chunks rather than all at once: a 500 MB file read into memory
    /// to send it is a needless spike on a machine that is also running the
    /// user's actual work.
    static func send(
        _ url: URL, index: Int, total: Int, fromClipboard: Bool = false,
        post: (Wire.MessageType, [UInt8]) -> Void
    ) throws {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        let size = (attributes[.size] as? NSNumber)?.uint64Value ?? 0
        let name = url.lastPathComponent

        guard size <= maxFileSize else { throw SendError.tooLarge(name: name, bytes: size) }
        guard let handle = try? FileHandle(forReadingFrom: url) else {
            throw SendError.unreadable(name: name)
        }
        defer { try? handle.close() }

        post(.fileBegin, Wire.fileBeginBody(
            name: name, size: size, index: index, total: total, fromClipboard: fromClipboard
        ))
        var sent: UInt64 = 0
        while sent < size {
            guard let data = try handle.read(upToCount: chunkSize), !data.isEmpty else { break }
            post(.fileChunk, [UInt8](data))
            sent += UInt64(data.count)
        }
        guard sent == size else { throw SendError.unreadable(name: name) }
        post(.fileEnd, [])
    }

}

// MARK: - Receiving

extension FileTransfer {
    /// Writes incoming files into `dropDirectory`, one at a time.
    ///
    /// Mirrors the agent's receiver, including the name handling: the name
    /// comes off the network and is attacker-chosen, so it is reduced to a
    /// single path component that can only land inside the directory.
    final class Receiver {
        private var handle: FileHandle?
        private var url: URL?
        private var remaining: UInt64 = 0

        /// Collects the files of one copy, so they reach the pasteboard
        /// together once the last has arrived. A paste that produced files one
        /// at a time as they landed would be worse than useless.
        private(set) var batch: [URL] = []
        private var batchTotal = 0
        private var fromClipboard = false

        /// The files of a finished clipboard copy, or nil while one is still
        /// arriving or when the batch came from a drag.
        var completedClipboardBatch: [URL]? {
            guard fromClipboard, !batch.isEmpty, batch.count >= batchTotal else { return nil }
            return batch
        }

        /// Reduces a name from the other machine to a safe single component.
        static func safeName(_ raw: String) -> String? {
            var name = raw.replacingOccurrences(of: "\\", with: "/")
            name = (name as NSString).lastPathComponent
            name = name.trimmingCharacters(in: .whitespaces)
            while name.hasSuffix(".") || name.hasSuffix(" ") { name.removeLast() }

            guard !name.isEmpty, name != ".", name != ".." else { return nil }
            let forbidden = CharacterSet(charactersIn: "<>:\"/\\|?*")
            guard name.rangeOfCharacter(from: forbidden) == nil,
                  name.rangeOfCharacter(from: .controlCharacters) == nil,
                  name.utf8.count <= 240
            else { return nil }
            return name
        }

        func begin(name: String, size: UInt64, index: Int = 0, total: Int = 1,
                   fromClipboard: Bool = false) throws {
            discard()
            if index == 0 { batch.removeAll() }
            batchTotal = total
            self.fromClipboard = fromClipboard
            guard size <= FileTransfer.maxFileSize else {
                throw SendError.tooLarge(name: name, bytes: size)
            }
            guard let safe = FileTransfer.Receiver.safeName(name) else {
                throw SendError.unreadable(name: name)
            }

            let directory = FileTransfer.dropDirectory
            try FileManager.default.createDirectory(
                at: directory, withIntermediateDirectories: true,
                attributes: [.posixPermissions: 0o700]
            )
            let destination = FileTransfer.Receiver.uniqueURL(in: directory, named: safe)
            guard FileManager.default.createFile(atPath: destination.path, contents: nil) else {
                throw SendError.unreadable(name: safe)
            }
            handle = try FileHandle(forWritingTo: destination)
            url = destination
            remaining = size
        }

        func chunk(_ bytes: [UInt8]) throws {
            guard let handle else { throw SendError.unreadable(name: "file") }
            guard UInt64(bytes.count) <= remaining else {
                discard()
                throw SendError.unreadable(name: url?.lastPathComponent ?? "file")
            }
            try handle.write(contentsOf: Data(bytes))
            remaining -= UInt64(bytes.count)
        }

        /// Returns where the file landed.
        func end() throws -> URL {
            guard let handle, let url, remaining == 0 else {
                discard()
                throw SendError.unreadable(name: url?.lastPathComponent ?? "file")
            }
            try handle.close()
            self.handle = nil
            self.url = nil
            batch.append(url)
            return url
        }

        func discard() {
            try? handle?.close()
            if let url { try? FileManager.default.removeItem(at: url) }
            handle = nil
            url = nil
            remaining = 0
        }

        /// Avoids overwriting whatever is already there, the way a download does.
        private static func uniqueURL(in directory: URL, named name: String) -> URL {
            let candidate = directory.appendingPathComponent(name)
            guard FileManager.default.fileExists(atPath: candidate.path) else { return candidate }

            let extensionPart = (name as NSString).pathExtension
            let stem = (name as NSString).deletingPathExtension
            for n in 2 ..< 1000 {
                let suffixed = extensionPart.isEmpty ? "\(stem) (\(n))" : "\(stem) (\(n)).\(extensionPart)"
                let next = directory.appendingPathComponent(suffixed)
                if !FileManager.default.fileExists(atPath: next.path) { return next }
            }
            return directory.appendingPathComponent("\(UUID().uuidString)-\(name)")
        }
    }
}

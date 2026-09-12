import Foundation

/// Remembers which PCs this Mac has paired with.
///
/// This deliberately does *not* use the Keychain. A keychain item's ACL is
/// bound to the code identity that created it, and an ad-hoc signature changes
/// on every rebuild — so macOS treats each build as a different app and
/// demands the login password to hand the secret back. Being prompted for your
/// password to talk to a PC on your own LAN is absurd, and no amount of
/// entitlement fiddling fixes it without a stable Developer ID.
///
/// So the pairing lives in a 0600 file, exactly like the Windows agent's own
/// state. That is a real trade: anything already running as you can read it.
/// It buys nothing against that attacker anyway — a process running as you can
/// simply synthesise the input directly.
enum PairingStore {
    struct Pairing {
        var secret: Data
        var fingerprint: Data
    }

    static var fileURL: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        return base.appendingPathComponent("Porthmoss/pairings.json")
    }

    static func load(agent: String) -> Pairing? {
        guard let entry = all()[agent],
              let secret = Data(hex: entry.secret), secret.count == 32,
              let fingerprint = Data(hex: entry.fingerprint), fingerprint.count == 32
        else { return nil }
        return Pairing(secret: secret, fingerprint: fingerprint)
    }

    static func save(agent: String, secret: Data, fingerprint: Data) throws {
        guard secret.count == 32, fingerprint.count == 32 else {
            throw PairingStoreError.malformed
        }
        var entries = all()
        entries[agent] = Entry(secret: secret.hexString, fingerprint: fingerprint.hexString)
        try write(entries)
    }

    static func forget(agent: String) {
        var entries = all()
        guard entries.removeValue(forKey: agent) != nil else { return }
        try? write(entries)
    }

    // MARK: - Storage

    private struct Entry: Codable {
        var secret: String
        var fingerprint: String
    }

    private static func all() -> [String: Entry] {
        guard let data = try? Data(contentsOf: fileURL),
              let entries = try? JSONDecoder().decode([String: Entry].self, from: data)
        else { return [:] }
        return entries
    }

    private static func write(_ entries: [String: Entry]) throws {
        let url = fileURL
        let directory = url.deletingLastPathComponent()
        try FileManager.default.createDirectory(
            at: directory,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        let data = try encoder.encode(entries)

        // Write then tighten, rather than trusting the umask.
        try data.write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }
}

enum PairingStoreError: LocalizedError {
    case malformed

    var errorDescription: String? {
        "Pairing material must be 32 bytes each."
    }
}

private extension Data {
    var hexString: String { map { String(format: "%02x", $0) }.joined() }

    init?(hex: String) {
        guard hex.count % 2 == 0 else { return nil }
        var bytes = [UInt8]()
        bytes.reserveCapacity(hex.count / 2)
        var index = hex.startIndex
        while index < hex.endIndex {
            let next = hex.index(index, offsetBy: 2)
            guard let byte = UInt8(hex[index ..< next], radix: 16) else { return nil }
            bytes.append(byte)
            index = next
        }
        self.init(bytes)
    }
}

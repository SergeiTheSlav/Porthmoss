import Foundation

/// Remembers which PCs this Mac has paired with.
///
/// This deliberately does *not* use the Keychain. A keychain item's ACL is
/// bound to the code identity that created it, and an ad-hoc signature changes
/// on every rebuild, so macOS treats each build as a different app and
/// demands the login password to hand the secret back. Being prompted for your
/// password to talk to a PC on your own LAN is absurd, and no amount of
/// entitlement fiddling fixes it without a stable Developer ID.
///
/// So the pairing lives in a 0600 file, exactly like the Windows agent's own
/// state. That is a real trade: anything already running as you can read it.
/// It buys nothing against that attacker anyway, a process running as you can
/// simply synthesise the input directly.
enum PairingStore {
    struct Pairing {
        var secret: Data
        var fingerprint: Data
    }

    /// A PC this Mac has paired with before, as the UI lists it.
    struct SavedPC: Identifiable, Equatable {
        var host: String
        var port: UInt16
        var name: String
        var pairedAt: Date?

        var id: String { host }
        /// Falls back to the address when the agent never told us a name.
        var displayName: String { name.isEmpty ? host : name }
    }

    static var fileURL: URL {
        Settings.configDirectory.appendingPathComponent("pairings.json")
    }

    static func load(agent: String) -> Pairing? {
        guard let entry = all()[agent],
              let secret = Data(hex: entry.secret), secret.count == 32,
              let fingerprint = Data(hex: entry.fingerprint), fingerprint.count == 32
        else { return nil }
        return Pairing(secret: secret, fingerprint: fingerprint)
    }

    static func save(
        agent: String, port: UInt16, name: String, secret: Data, fingerprint: Data
    ) throws {
        guard secret.count == 32, fingerprint.count == 32 else {
            throw PairingStoreError.malformed
        }
        var entries = all()
        entries[agent] = Entry(
            secret: secret.hexString,
            fingerprint: fingerprint.hexString,
            name: name,
            port: port,
            pairedAt: ISO8601DateFormatter().string(from: Date())
        )
        try write(entries)
    }

    /// Every PC this Mac has paired with, most recently paired first.
    static func saved() -> [SavedPC] {
        let parser = ISO8601DateFormatter()
        return all().map { host, entry in
            SavedPC(
                host: host,
                port: entry.port ?? 47654,
                name: entry.name ?? "",
                pairedAt: entry.pairedAt.flatMap(parser.date(from:))
            )
        }
        .sorted { ($0.pairedAt ?? .distantPast) > ($1.pairedAt ?? .distantPast) }
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
        // Added after the first version, so all of these are optional: an
        // older file must still load rather than dropping the pairing.
        var name: String?
        var port: UInt16?
        var pairedAt: String?
    }

    private static func all() -> [String: Entry] {
        guard let data = try? Data(contentsOf: fileURL),
              let entries = try? JSONDecoder().decode([String: Entry].self, from: data)
        else { return [:] }
        return migrateInterfaceScopes(entries)
    }

    /// Earlier versions saved addresses with the interface scope Bonjour
    /// reports, "192.168.0.7%en0". That pins a pairing to one interface, so
    /// the same PC over Ethernet rather than Wi-Fi looked like a different,
    /// unpaired machine. Rewrite them in place rather than making the user
    /// pair again.
    private static func migrateInterfaceScopes(_ entries: [String: Entry]) -> [String: Entry] {
        var migrated: [String: Entry] = [:]
        var changed = false
        for (host, entry) in entries {
            guard let percent = host.firstIndex(of: "%") else {
                migrated[host] = entry
                continue
            }
            let bare = String(host[..<percent])
            // Only IPv4: an IPv6 link-local address needs its zone to be usable.
            guard bare.split(separator: ".").count == 4,
                  bare.split(separator: ".").allSatisfy({ UInt8($0) != nil })
            else {
                migrated[host] = entry
                continue
            }
            migrated[bare] = entry
            changed = true
        }
        if changed { try? write(migrated) }
        return migrated
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

import Foundation
import Security

/// Stores the pairing secret and the pinned fingerprint in the login keychain.
///
/// These two together are exactly what is needed to take over the Windows PC,
/// so they do not belong in a plist next to the preferences.
enum Keychain {
    private static let service = "app.porthmoss.pairing"

    struct Pairing {
        var secret: Data
        var fingerprint: Data
    }

    static func load(agent: String) -> Pairing? {
        guard let blob = read(account: agent), blob.count == 64 else { return nil }
        return Pairing(secret: blob.prefix(32), fingerprint: blob.suffix(32))
    }

    static func save(agent: String, secret: Data, fingerprint: Data) throws {
        guard secret.count == 32, fingerprint.count == 32 else {
            throw NSError(domain: service, code: -1, userInfo: [
                NSLocalizedDescriptionKey: "pairing material must be 32 bytes each",
            ])
        }
        try write(account: agent, blob: secret + fingerprint)
    }

    static func forget(agent: String) {
        SecItemDelete(query(account: agent) as CFDictionary)
    }

    private static func query(account: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }

    private static func read(account: String) -> Data? {
        var lookup = query(account: account)
        lookup[kSecReturnData as String] = true
        lookup[kSecMatchLimit as String] = kSecMatchLimitOne

        var result: CFTypeRef?
        guard SecItemCopyMatching(lookup as CFDictionary, &result) == errSecSuccess else { return nil }
        return result as? Data
    }

    private static func write(account: String, blob: Data) throws {
        SecItemDelete(query(account: account) as CFDictionary)
        var item = query(account: account)
        item[kSecValueData as String] = blob
        // Only readable while the Mac is unlocked, and never synced to iCloud.
        item[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly

        let status = SecItemAdd(item as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw NSError(domain: service, code: Int(status), userInfo: [
                NSLocalizedDescriptionKey: "could not save pairing to the keychain (OSStatus \(status))",
            ])
        }
    }
}

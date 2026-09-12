import CryptoKit
import Foundation

/// The Mac half of pairing. Must stay byte-for-byte compatible with
/// `win/internal/pairing`.
enum Pairing {
    static let info = "porthmoss-v1-pairing"
    static let secretSize = 32

    /// Turns the 6-digit code the agent displays into the shared secret.
    ///
    /// Salting with the certificate fingerprint is what makes a short code
    /// safe: the same code typed at a different machine derives a different
    /// secret, so it cannot be replayed against an impostor agent.
    static func deriveSecret(code: String, fingerprint: Data) -> Data {
        let key = HKDF<SHA256>.deriveKey(
            inputKeyMaterial: SymmetricKey(data: Data(code.utf8)),
            salt: fingerprint,
            info: Data(info.utf8),
            outputByteCount: secretSize
        )
        return key.withUnsafeBytes { Data($0) }
    }

    static func sign(secret: Data, nonce: Data) -> Data {
        Data(HMAC<SHA256>.authenticationCode(for: nonce, using: SymmetricKey(data: secret)))
    }
}

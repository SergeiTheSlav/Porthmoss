import Foundation

/// Big-endian helpers shared by the codec. Kept explicit rather than using
/// `withUnsafeBytes` tricks so the wire format is readable at the call site.
extension Array where Element == UInt8 {
    mutating func appendBigEndian(_ value: UInt16) {
        append(UInt8(truncatingIfNeeded: value >> 8))
        append(UInt8(truncatingIfNeeded: value))
    }

    mutating func appendBigEndian(_ value: UInt32) {
        for shift in stride(from: 24, through: 0, by: -8) {
            append(UInt8(truncatingIfNeeded: value >> UInt32(shift)))
        }
    }

    mutating func appendBigEndian(_ value: UInt64) {
        for shift in stride(from: 56, through: 0, by: -8) {
            append(UInt8(truncatingIfNeeded: value >> UInt64(shift)))
        }
    }

    func readBigEndianInt32(at index: Int) -> Int32 {
        var value: UInt32 = 0
        for offset in 0 ..< 4 { value = value << 8 | UInt32(self[index + offset]) }
        return Int32(bitPattern: value)
    }
}

extension Data {
    mutating func appendBigEndian(_ value: UInt32) {
        for shift in stride(from: 24, through: 0, by: -8) {
            append(UInt8(truncatingIfNeeded: value >> UInt32(shift)))
        }
    }

    func readBigEndianUInt32(at offset: Int) -> UInt32 {
        var value: UInt32 = 0
        for i in 0 ..< 4 { value = value << 8 | UInt32(self[startIndex + offset + i]) }
        return value
    }
}

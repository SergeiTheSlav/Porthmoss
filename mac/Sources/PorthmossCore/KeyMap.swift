import Foundation

/// A Windows key, as a PS/2 set-1 scancode plus whether it needs the E0 prefix.
public struct Scancode: Equatable, Sendable {
    public let code: UInt16
    public let extended: Bool

    public init(_ code: UInt16, extended: Bool = false) {
        self.code = code
        self.extended = extended
    }
}

/// How the Mac's modifiers should appear to Windows.
///
/// The default follows what Synergy and Logitech Flow do, and it is the whole
/// reason this feels native: Cmd becomes Ctrl, so Cmd+C, Cmd+T and Cmd+W all
/// keep working from muscle memory. Mac Control then has to go somewhere, and
/// the Windows key is the natural home — it plays the same "system" role Cmd
/// plays on macOS.
public struct ModifierMapping: Sendable, Equatable {
    public var command: Scancode
    public var control: Scancode
    public var option: Scancode
    public var shift: Scancode

    public static let `default` = ModifierMapping(
        command: Scancode(0x1D),              // left Ctrl
        control: Scancode(0x5B, extended: true), // left Win
        option: Scancode(0x38),               // left Alt
        shift: Scancode(0x2A)                 // left Shift
    )

    /// Straight-through mapping for people who want Mac Ctrl to stay Ctrl.
    public static let passthrough = ModifierMapping(
        command: Scancode(0x5B, extended: true),
        control: Scancode(0x1D),
        option: Scancode(0x38),
        shift: Scancode(0x2A)
    )

    public init(command: Scancode, control: Scancode, option: Scancode, shift: Scancode) {
        self.command = command; self.control = control
        self.option = option; self.shift = shift
    }
}

/// Translates macOS virtual key codes into Windows scancodes.
///
/// We send scancodes rather than virtual key codes so the agent never has to
/// know which keyboard layout the Mac is using — Windows applies the layout
/// the user selected on the PC, which is what they expect.
public enum KeyMap {
    /// Mac virtual keycodes for the modifier keys, which arrive as
    /// `flagsChanged` events rather than key down/up.
    public enum Modifier: UInt16, CaseIterable, Sendable {
        case leftCommand = 0x37
        case rightCommand = 0x36
        case leftShift = 0x38
        case rightShift = 0x3C
        case leftOption = 0x3A
        case rightOption = 0x3D
        case leftControl = 0x3B
        case rightControl = 0x3E
        case capsLock = 0x39
    }

    /// Maps a modifier through `mapping`, keeping left/right sidedness where
    /// Windows distinguishes it.
    public static func scancode(for modifier: Modifier, mapping: ModifierMapping) -> Scancode {
        switch modifier {
        case .leftCommand: return mapping.command
        case .rightCommand: return rightVariant(of: mapping.command)
        case .leftControl: return mapping.control
        case .rightControl: return rightVariant(of: mapping.control)
        case .leftOption: return mapping.option
        case .rightOption: return rightVariant(of: mapping.option)
        case .leftShift: return mapping.shift
        case .rightShift: return Scancode(0x36)
        case .capsLock: return Scancode(0x3A)
        }
    }

    /// Windows encodes the right-hand modifiers as the E0-prefixed form of the
    /// left one, except Shift, which has a scancode of its own.
    private static func rightVariant(of left: Scancode) -> Scancode {
        switch (left.code, left.extended) {
        case (0x1D, false): return Scancode(0x1D, extended: true) // Ctrl -> RCtrl
        case (0x38, false): return Scancode(0x38, extended: true) // Alt  -> RAlt
        case (0x5B, true): return Scancode(0x5C, extended: true)  // LWin -> RWin
        case (0x2A, false): return Scancode(0x36)                 // Shift -> RShift
        default: return left
        }
    }

    /// Maps a non-modifier macOS virtual keycode. Returns nil for keys with no
    /// Windows equivalent (Fn, the media keys, and friends).
    public static func scancode(forVirtualKey keycode: UInt16) -> Scancode? {
        table[keycode]
    }

    private static let table: [UInt16: Scancode] = [
        // Letters
        0x00: Scancode(0x1E), 0x0B: Scancode(0x30), 0x08: Scancode(0x2E), 0x02: Scancode(0x20),
        0x0E: Scancode(0x12), 0x03: Scancode(0x21), 0x05: Scancode(0x22), 0x04: Scancode(0x23),
        0x22: Scancode(0x17), 0x26: Scancode(0x24), 0x28: Scancode(0x25), 0x25: Scancode(0x26),
        0x2E: Scancode(0x32), 0x2D: Scancode(0x31), 0x1F: Scancode(0x18), 0x23: Scancode(0x19),
        0x0C: Scancode(0x10), 0x0F: Scancode(0x13), 0x01: Scancode(0x1F), 0x11: Scancode(0x14),
        0x20: Scancode(0x16), 0x09: Scancode(0x2F), 0x0D: Scancode(0x11), 0x07: Scancode(0x2D),
        0x10: Scancode(0x15), 0x06: Scancode(0x2C),

        // Digits
        0x12: Scancode(0x02), 0x13: Scancode(0x03), 0x14: Scancode(0x04), 0x15: Scancode(0x05),
        0x17: Scancode(0x06), 0x16: Scancode(0x07), 0x1A: Scancode(0x08), 0x1C: Scancode(0x09),
        0x19: Scancode(0x0A), 0x1D: Scancode(0x0B),

        // Punctuation
        0x1B: Scancode(0x0C), 0x18: Scancode(0x0D), 0x21: Scancode(0x1A), 0x1E: Scancode(0x1B),
        0x2A: Scancode(0x2B), 0x29: Scancode(0x27), 0x27: Scancode(0x28), 0x32: Scancode(0x29),
        0x2B: Scancode(0x33), 0x2F: Scancode(0x34), 0x2C: Scancode(0x35),

        // Control keys
        0x24: Scancode(0x1C), 0x30: Scancode(0x0F), 0x31: Scancode(0x39), 0x33: Scancode(0x0E),
        0x35: Scancode(0x01),

        // Function row
        0x7A: Scancode(0x3B), 0x78: Scancode(0x3C), 0x63: Scancode(0x3D), 0x76: Scancode(0x3E),
        0x60: Scancode(0x3F), 0x61: Scancode(0x40), 0x62: Scancode(0x41), 0x64: Scancode(0x42),
        0x65: Scancode(0x43), 0x6D: Scancode(0x44), 0x67: Scancode(0x57), 0x6F: Scancode(0x58),

        // Navigation — all E0-prefixed on Windows
        0x7B: Scancode(0x4B, extended: true), 0x7C: Scancode(0x4D, extended: true),
        0x7E: Scancode(0x48, extended: true), 0x7D: Scancode(0x50, extended: true),
        0x73: Scancode(0x47, extended: true), 0x77: Scancode(0x4F, extended: true),
        0x74: Scancode(0x49, extended: true), 0x79: Scancode(0x51, extended: true),
        0x75: Scancode(0x53, extended: true), 0x72: Scancode(0x52, extended: true),

        // Numeric keypad
        0x52: Scancode(0x52), 0x53: Scancode(0x4F), 0x54: Scancode(0x50), 0x55: Scancode(0x51),
        0x56: Scancode(0x4B), 0x57: Scancode(0x4C), 0x58: Scancode(0x4D), 0x59: Scancode(0x47),
        0x5B: Scancode(0x48), 0x5C: Scancode(0x49), 0x41: Scancode(0x53), 0x45: Scancode(0x4E),
        0x4E: Scancode(0x4A), 0x43: Scancode(0x37), 0x4B: Scancode(0x35, extended: true),
        0x4C: Scancode(0x1C, extended: true),
    ]
}

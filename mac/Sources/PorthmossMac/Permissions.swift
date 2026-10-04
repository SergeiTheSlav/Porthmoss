import ApplicationServices
import Foundation
import IOKit.hid

/// The two TCC grants the event tap needs, checked separately.
///
/// They are genuinely different permissions in different System Settings
/// panes, and a tap fails identically whichever one is missing, so telling
/// the user "grant both" when they have already granted one is useless.
enum Permissions {
    enum Status {
        case granted, denied, undetermined
    }

    /// Accessibility, which is what allows an event tap to *modify* events.
    static var accessibility: Status {
        AXIsProcessTrusted() ? .granted : .denied
    }

    /// Input Monitoring, which is what allows it to see keystrokes at all.
    static var inputMonitoring: Status {
        switch IOHIDCheckAccess(kIOHIDRequestTypeListenEvent) {
        case kIOHIDAccessTypeGranted: return .granted
        case kIOHIDAccessTypeDenied: return .denied
        default: return .undetermined
        }
    }

    /// Asks for anything not yet granted. Both calls are no-ops once the user
    /// has answered, and the Accessibility one only ever opens Settings.
    static func request() {
        if inputMonitoring != .granted {
            _ = IOHIDRequestAccess(kIOHIDRequestTypeListenEvent)
        }
        if accessibility != .granted {
            // The constant is a global var in the C header and so is not
            // Sendable; its value is a documented, stable string.
            let options = ["AXTrustedCheckOptionPrompt": true]
            _ = AXIsProcessTrustedWithOptions(options as CFDictionary)
        }
    }

    /// What to tell the user, or nil when both are in place.
    static var problem: String? {
        let missing = [
            accessibility != .granted ? "Accessibility" : nil,
            inputMonitoring != .granted ? "Input Monitoring" : nil,
        ].compactMap { $0 }

        guard !missing.isEmpty else { return nil }
        let list = missing.joined(separator: " and ")
        return """
        Porthmoss needs \(list) in System Settings › Privacy & Security.

        If it is already listed there, select it, remove it with −, and add \
        /Applications/Porthmoss.app again. macOS ties the grant to the app's \
        signature, and will not honour one made to an earlier signature even \
        though the switch still looks enabled.
        """
    }
}

import Foundation
import PorthmossCore

/// User-facing configuration, persisted as JSON so it stays hand-editable
/// while there is no settings UI yet.
struct Settings: Equatable {
    var agentHost = ""
    var agentPort: UInt16 = 47654
    var clientName = Host.current().localizedName ?? "Mac"

    var capture = CaptureConfig()
    var modifiers = ModifierMapping.default

    /// Which display's edge leads to the PC, as a `Display.id`.
    ///
    /// Empty means "whichever display has that edge on the outside of the
    /// whole desktop", which is right for a single-screen Mac and is what
    /// every Mac did before this was a choice. It matters as soon as two
    /// displays both have, say, a free right edge: one of them is where the
    /// PC is, and the other is where the user reaches for a scrollbar.
    var crossingDisplay = ""

    /// macOS already applies "natural scrolling" before we see the event, so
    /// this is only for people who want the Windows side to differ.
    var invertScroll = false
    /// How much trackpad movement makes one Windows wheel notch.
    var pixelsPerNotch = 12.0


    /// Where configuration lives.
    ///
    /// PORTHMOSS_CONFIG_DIR redirects it, which the test scripts use. They
    /// drive this Mac with synthetic input, and synthetic keystrokes land in
    /// whatever has focus — more than once that was the settings panel, which
    /// silently changed the user's crossing edge and then failed a test that
    /// had nothing to do with it.
    static var configDirectory: URL {
        if let override = ProcessInfo.processInfo.environment["PORTHMOSS_CONFIG_DIR"] {
            return URL(fileURLWithPath: override, isDirectory: true)
        }
        return FileManager.default
            .urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Porthmoss")
    }

    static var fileURL: URL {
        configDirectory.appendingPathComponent("settings.json")
    }

    static func load() -> Settings {
        var settings = Settings()
        guard let data = try? Data(contentsOf: fileURL),
              let stored = try? JSONDecoder().decode(Stored.self, from: data)
        else { return settings }

        settings.agentHost = stored.agentHost ?? settings.agentHost
        settings.agentPort = stored.agentPort ?? settings.agentPort
        settings.clientName = stored.clientName ?? settings.clientName
        settings.invertScroll = stored.invertScroll ?? settings.invertScroll
        settings.pixelsPerNotch = stored.pixelsPerNotch ?? settings.pixelsPerNotch
        settings.crossingDisplay = stored.crossingDisplay ?? settings.crossingDisplay
        if let edge = stored.edge.flatMap(ScreenEdge.init(rawValue:)) { settings.capture.edge = edge }
        if let value = stored.sensitivity { settings.capture.sensitivity = value }
        if let value = stored.pushThreshold { settings.capture.pushThreshold = value }
        if stored.passthroughModifiers == true { settings.modifiers = .passthrough }
        return settings
    }

    func save() throws {
        let stored = Stored(
            agentHost: agentHost, agentPort: agentPort, clientName: clientName,
            edge: capture.edge.rawValue, sensitivity: capture.sensitivity,
            pushThreshold: capture.pushThreshold, invertScroll: invertScroll,
            pixelsPerNotch: pixelsPerNotch,
            passthroughModifiers: modifiers.control.code == 0x1D,
            crossingDisplay: crossingDisplay
        )
        let url = Settings.fileURL
        try FileManager.default.createDirectory(
            at: url.deletingLastPathComponent(), withIntermediateDirectories: true
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        try encoder.encode(stored).write(to: url, options: .atomic)
    }

    private struct Stored: Codable {
        var agentHost: String?
        var agentPort: UInt16?
        var clientName: String?
        var edge: String?
        var sensitivity: Double?
        var pushThreshold: Double?
        var invertScroll: Bool?
        var pixelsPerNotch: Double?
        var passthroughModifiers: Bool?
        var crossingDisplay: String?
    }
}

import Foundation

/// Who wrote this, what it may be used for, and what it is built on.
///
/// The version lives here and nowhere else on the Mac side: `make-app.sh`
/// reads it out of this file to stamp the bundle, so there is one string to
/// change for a release. `win/internal/about` carries the same numbers for the
/// agent, and `make check-version` fails the build if the two drift apart.
public enum About {
    public static let name = "Porthmoss"
    public static let version = "0.2.0"
    public static let author = "Jan Jamscikov"
    public static let repository = "https://github.com/SergeiTheSlav/Porthmoss"
    public static let licence = "MIT"
    public static let copyright = "© 2026 Jan Jamscikov"

    /// One line, for a window title bar or a log header.
    public static var summary: String {
        "\(name) \(version), \(licence) licence, \(repository)"
    }

    /// Something the project is built on and owes a mention to.
    public struct Credit: Sendable, Identifiable {
        public let name: String
        /// What it does here, so the list is informative rather than legal
        /// boilerplate nobody reads.
        public let role: String
        public let licence: String
        public let url: String

        public var id: String { name }

        public init(name: String, role: String, licence: String, url: String) {
            self.name = name
            self.role = role
            self.licence = licence
            self.url = url
        }
    }

    /// Everything the Windows agent links in. The Mac app has no third-party
    /// dependencies at all, it is SwiftUI, CoreGraphics and Network, so this
    /// list is the agent's, and is shown on both sides because the two halves
    /// are one product.
    public static let acknowledgements: [Credit] = [
        Credit(name: "energye/systray", role: "the agent's notification-area icon",
               licence: "Apache 2.0", url: "https://github.com/energye/systray"),
        Credit(name: "jchv/go-webview2", role: "the agent's window",
               licence: "MIT", url: "https://github.com/jchv/go-webview2"),
        Credit(name: "jchv/go-winloader", role: "loading WebView2 without a DLL on disk",
               licence: "ISC", url: "https://github.com/jchv/go-winloader"),
        Credit(name: "libp2p/zeroconf", role: "finding the PC on the network",
               licence: "MIT", url: "https://github.com/libp2p/zeroconf"),
        Credit(name: "miekg/dns", role: "the DNS records mDNS discovery is made of",
               licence: "BSD 3-clause", url: "https://github.com/miekg/dns"),
        Credit(name: "golang.org/x/sys", role: "the Win32 calls the agent makes",
               licence: "BSD 3-clause", url: "https://pkg.go.dev/golang.org/x/sys"),
    ]
}

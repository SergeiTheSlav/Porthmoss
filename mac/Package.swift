// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Porthmoss",
    platforms: [.macOS(.v14)],
    targets: [
        // Pure logic: wire protocol, coordinate mapping, key translation, edge
        // detection. No system frameworks, so it is all unit-testable.
        .target(name: "PorthmossCore"),

        // Everything that touches the OS: the event tap, the pinned TLS
        // connection, cursor control, and the menu bar app.
        .executableTarget(
            name: "PorthmossMac",
            dependencies: ["PorthmossCore"]
        ),
        .testTarget(name: "PorthmossCoreTests", dependencies: ["PorthmossCore"]),
    ]
)

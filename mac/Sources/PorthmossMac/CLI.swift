import Foundation
import PorthmossCore

/// The headless front end, reached with `--cli`.
///
/// The GUI is the product; this stays because it is the only way to exercise
/// the link over SSH or from a test harness, with no window server involved.
enum CLI {
    /// Held so the session outlives `run`'s stack frame. Only ever touched on
    /// the main run loop, where the tap callback and the connection's hops land.
    nonisolated(unsafe) private static var activeSession: Session?

    static func run(settings initial: Settings) -> Never {
        setlinebuf(stdout)


        var settings = initial
        var args = Array(CommandLine.arguments.dropFirst())
        var shouldUnpair = false
        var discoverOnly = false

        func usage() -> Never {
            print("""
            porthmoss — control a Windows PC from this Mac

            USAGE
              porthmoss [options]

            OPTIONS
              --host <addr>        agent address (default: discover on the network)
              --port <n>           agent port (default: \(settings.agentPort))
              --edge <side>        which edge leads to the PC: left|right|top|bottom
              --sensitivity <n>    mouse scaling on the PC (default: \(settings.capture.sensitivity))
              --push <points>      how hard to push through the edge (default: \(Int(settings.capture.pushThreshold)))
              --passthrough        keep Mac Ctrl as Windows Ctrl (default: Cmd becomes Ctrl)
              --invert-scroll      flip scroll direction on the PC
              --discover           list agents on the network and exit
              --unpair             forget the stored pairing and exit
              --save               write the current options to \(Settings.fileURL.path)
              -h, --help           this message

            While controlling the PC, Ctrl+Option+Cmd+P always hands control back.
            """)
            exit(0)
        }

        var shouldSave = false
        while let arg = args.first {
            args.removeFirst()
            func value(_ name: String) -> String {
                guard let next = args.first else {
                    FileHandle.standardError.write(Data("porthmoss: \(name) needs a value\n".utf8))
                    exit(2)
                }
                args.removeFirst()
                return next
            }
            switch arg {
            case "--host": settings.agentHost = value(arg)
            case "--port": settings.agentPort = UInt16(value(arg)) ?? settings.agentPort
            case "--edge":
                let raw = value(arg)
                guard let edge = ScreenEdge(rawValue: raw) else {
                    FileHandle.standardError.write(Data("porthmoss: unknown edge '\(raw)'\n".utf8))
                    exit(2)
                }
                settings.capture.edge = edge
            case "--sensitivity": settings.capture.sensitivity = Double(value(arg)) ?? settings.capture.sensitivity
            case "--push": settings.capture.pushThreshold = Double(value(arg)) ?? settings.capture.pushThreshold
            case "--passthrough": settings.modifiers = .passthrough
            case "--invert-scroll": settings.invertScroll = true
            case "--discover": discoverOnly = true
            case "--unpair": shouldUnpair = true
            case "--save": shouldSave = true
            case "-h", "--help": usage()
            default:
                FileHandle.standardError.write(Data("porthmoss: unknown option '\(arg)'\n".utf8))
                exit(2)
            }
        }

        if shouldSave {
            try? settings.save()
            print("Saved to \(Settings.fileURL.path)")
        }

        if shouldUnpair {
            guard !settings.agentHost.isEmpty else {
                print("porthmoss: --unpair needs --host to say which agent to forget")
                exit(2)
            }
            Keychain.forget(agent: settings.agentHost)
            print("Forgot the pairing with \(settings.agentHost). Run the agent with --unpair too.")
            exit(0)
        }

        // MARK: - Find the agent

        func discoverBlocking(timeout: TimeInterval) -> [Discovery.Agent] {
            let box = ResultBox()
            Discovery.find(timeout: timeout) { agents in
                box.agents = agents
                CFRunLoopStop(CFRunLoopGetMain())
            }
            CFRunLoopRunInMode(.defaultMode, timeout + 1, false)
            return box.agents
        }

        final class ResultBox: @unchecked Sendable {
            var agents: [Discovery.Agent] = []
        }

        if discoverOnly {
            print("Looking for Porthmoss agents…")
            let agents = discoverBlocking(timeout: 3)
            if agents.isEmpty {
                print("None found. Check the agent is running and both machines are on the same network.")
            }
            for agent in agents { print("  \(agent.name) — \(agent.host):\(agent.port)") }
            exit(0)
        }

        if settings.agentHost.isEmpty {
            print("Looking for Porthmoss agents…")
            let agents = discoverBlocking(timeout: 3)
            guard let agent = agents.first else {
                print("""
                No agent found.

                Start porthmoss-agent.exe on the Windows PC, or pass its address with
                --host if the two machines cannot see each other over mDNS.
                """)
                exit(1)
            }
            if agents.count > 1 {
                print("Found \(agents.count) agents; using \(agent.name). Pass --host to pick another.")
            }
            settings.agentHost = agent.host
            settings.agentPort = agent.port
        }

        // MARK: - Connect

        let stored = Keychain.load(agent: settings.agentHost)
        let connection = AgentConnection(
            host: settings.agentHost,
            port: settings.agentPort,
            pinnedFingerprint: stored?.fingerprint
        )

        print("Connecting to \(settings.agentHost):\(settings.agentPort)…")

        let agentHost = settings.agentHost
        let sessionSettings = settings

        connection.start(
            clientName: settings.clientName,
            storedSecret: stored?.secret,
            codeProvider: { _ in
                // Runs on the connection queue, which is exactly where we want to block
                // while the user reads the code off the PC and types it here.
                print("""

                This Mac is not paired with that PC yet.
                A 6-digit code is showing in the agent's window.

                """)
                print("Pairing code: ", terminator: "")
                return readLine(strippingNewline: true)?.trimmingCharacters(in: .whitespaces)
            },
            completion: { result in
                DispatchQueue.main.async {
                    switch result {
                    case let .failure(error):
                        let message = (error as? WireError).map(describe) ?? error.localizedDescription
                        FileHandle.standardError.write(Data("porthmoss: \(message)\n".utf8))
                        exit(1)

                    case let .success((screens, secret, fingerprint)):
                        do {
                            try Keychain.save(agent: agentHost, secret: secret, fingerprint: fingerprint)
                        } catch {
                            print("warning: could not store the pairing — \(error.localizedDescription)")
                        }
                        print("""
                        Connected to \(agentHost). \
                        Windows desktop is \(screens.virtualDesktop.width)×\(screens.virtualDesktop.height) \
                        across \(screens.monitors.count) monitor(s).
                        """)

                        let session = Session(connection: connection, screens: screens, settings: sessionSettings)
                        do {
                            try session.start()
                        } catch let error as EventTap.StartError {
                            FileHandle.standardError.write(Data("\n\(error.description)\n".utf8))
                            exit(1)
                        } catch {
                            FileHandle.standardError.write(Data("porthmoss: \(error.localizedDescription)\n".utf8))
                            exit(1)
                        }
                        CLI.activeSession = session
                    }
                }
            }
        )



        signal(SIGINT) { _ in
            print("\nStopping.")
            exit(0)
        }

        CFRunLoopRun()
        exit(0)
    }

    private static func describe(_ error: WireError) -> String {
        switch error {
        case .truncated: return "the agent sent a malformed message"
        case let .frameTooLarge(size): return "the agent sent an oversized message (\(size) bytes)"
        case let .unknownType(type): return "unknown message type 0x\(String(type, radix: 16))"
        case let .versionMismatch(version): return "the agent speaks protocol v\(version); this Mac speaks v\(Wire.version)"
        case let .rejected(reason): return reason
    }
        }
}

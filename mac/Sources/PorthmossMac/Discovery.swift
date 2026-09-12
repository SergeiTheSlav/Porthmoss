import Foundation
import Network

/// Finds Porthmoss agents on the local network, so the first run does not
/// require typing an IP address.
enum Discovery {
    struct Agent: Sendable {
        var name: String
        var host: String
        var port: UInt16
    }

    /// Browses for `_porthmoss._tcp` and resolves what it finds, giving up
    /// after `timeout`.
    static func find(timeout: TimeInterval, completion: @escaping @Sendable ([Agent]) -> Void) {
        let browser = NWBrowser(
            for: .bonjour(type: "_porthmoss._tcp", domain: nil),
            using: .tcp
        )
        let found = FoundAgents()

        browser.browseResultsChangedHandler = { results, _ in
            for result in results {
                guard case let .service(name, type, domain, _) = result.endpoint else { continue }
                resolve(name: name, type: type, domain: domain) { agent in
                    if let agent { found.append(agent) }
                }
            }
        }
        browser.start(queue: .main)

        DispatchQueue.main.asyncAfter(deadline: .now() + timeout) {
            browser.cancel()
            completion(found.all)
        }
    }

    /// Bonjour gives us a service name; a connection attempt is what turns it
    /// into an address we can pin and dial.
    private static func resolve(
        name: String, type: String, domain: String,
        completion: @escaping @Sendable (Agent?) -> Void
    ) {
        let endpoint = NWEndpoint.service(name: name, type: type, domain: domain, interface: nil)
        let connection = NWConnection(to: endpoint, using: .tcp)
        connection.stateUpdateHandler = { state in
            switch state {
            case .ready:
                defer { connection.cancel() }
                guard case let .hostPort(host, port)? = connection.currentPath?.remoteEndpoint else {
                    return completion(nil)
                }
                completion(Agent(name: name, host: displayString(host), port: port.rawValue))
            case .failed, .cancelled:
                completion(nil)
            default:
                break
            }
        }
        connection.start(queue: .main)
    }

    private static func displayString(_ host: NWEndpoint.Host) -> String {
        switch host {
        case let .name(name, _): return name
        case let .ipv4(address): return "\(address)"
        case let .ipv6(address): return "\(address)"
        @unknown default: return "\(host)"
        }
    }
}

private final class FoundAgents: @unchecked Sendable {
    private var agents: [String: Discovery.Agent] = [:]
    func append(_ agent: Discovery.Agent) { agents[agent.name] = agent }
    var all: [Discovery.Agent] { Array(agents.values) }
}

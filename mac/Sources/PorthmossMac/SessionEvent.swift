import Foundation

/// What a session reports upward.
///
/// This was a string, and the app recovered from a dropped PC by matching the
/// prefix of a human-readable sentence. Two things were wrong with that:
/// rewording a message silently broke the release path, and every message that
/// was *not* "Controlling…" was read as "no longer controlling" — so dragging a
/// file across, which reports "Sending…", flipped the menu bar back mid-session.
enum SessionEvent {
    /// The link is up and idle.
    case ready(String)
    /// This Mac began driving the PC.
    case startedDrivingPC
    /// This Mac stopped driving the PC, for the stated reason.
    case stoppedDrivingPC(String)
    /// The PC began driving this Mac.
    case startedBeingDriven
    /// The PC stopped driving this Mac, for the stated reason.
    case stoppedBeingDriven(String)
    /// The link is gone and the session is over.
    case connectionLost(String)
    /// Worth showing, but it changes no control state. Transfers, warnings,
    /// and anything else that must not be mistaken for a change of direction.
    case note(String)

    /// What the user reads.
    var message: String {
        switch self {
        case let .ready(text): return text
        case .startedDrivingPC: return "Controlling the PC."
        case let .stoppedDrivingPC(reason): return reason
        case .startedBeingDriven: return "The PC is controlling this Mac."
        case let .stoppedBeingDriven(reason): return reason
        case let .connectionLost(reason): return "Connection lost: \(reason)"
        case let .note(text): return text
        }
    }
}

/// Which way input is flowing, if at all.
enum ControlDirection {
    case none
    /// This Mac's keyboard and mouse are driving the PC.
    case drivingPC
    /// The PC's keyboard and mouse are driving this Mac.
    case drivenByPC
}

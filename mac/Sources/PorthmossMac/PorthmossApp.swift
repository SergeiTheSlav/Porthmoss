import SwiftUI

@main
struct PorthmossMain {
    static func main() {
        var settings = Settings.load()
        let arguments = Array(CommandLine.arguments.dropFirst())

        // --cli keeps the headless path available for SSH sessions and test
        // harnesses, where there is no window server to talk to.
        if arguments.contains("--cli") || arguments.contains("--discover") {
            CLI.run(settings: settings)
        }
        // Let --host prefill the GUI, so a launch agent can point at a fixed PC.
        if let index = arguments.firstIndex(of: "--host"), index + 1 < arguments.count {
            settings.agentHost = arguments[index + 1]
        }
        AppLaunch.settings = settings
        PorthmossApp.main()
    }
}

/// Carries the parsed command line from `main` into the App's `init`, which
/// SwiftUI calls with no arguments of its own.
enum AppLaunch {
    nonisolated(unsafe) static var settings = Settings()
}

struct PorthmossApp: App {
    @StateObject private var model: AppModel
    @Environment(\.openWindow) private var openWindow

    init() {
        _model = StateObject(wrappedValue: AppModel(settings: AppLaunch.settings))
    }

    var body: some Scene {
        Window("Porthmoss", id: "main") {
            MainWindow()
                .environmentObject(model)
                .frame(width: 460)
                .background(WindowBackground())
        }
        // The window hugs its content and grows when Settings opens, rather
        // than leaving a field of empty glass below the last panel.
        .windowResizability(.contentSize)

        MenuBarExtra {
            MenuBarContent { openWindow(id: "main") }
                .environmentObject(model)
        } label: {
            // Filled while the PC is being driven, so the menu bar answers
            // "where is my keyboard going?" without opening anything.
            Image(systemName: model.isControllingPC
                  ? "arrow.left.arrow.right.circle.fill"
                  : (model.isConnected ? "arrow.left.arrow.right.circle"
                                       : "arrow.left.arrow.right"))
        }
    }
}

/// A translucent window backing, so Liquid Glass panels have something to
/// float over rather than a flat sheet of grey.
private struct WindowBackground: NSViewRepresentable {
    func makeNSView(context: Context) -> NSVisualEffectView {
        let view = NSVisualEffectView()
        // .hudWindow reads as a floating panel, which gives the glass
        // surfaces something to actually sit on top of. .underWindowBackground
        // is so close to opaque that the panels looked like flat grey cards.
        view.material = .hudWindow
        view.blendingMode = .behindWindow
        view.state = .active
        return view
    }

    func updateNSView(_ view: NSVisualEffectView, context: Context) {}
}

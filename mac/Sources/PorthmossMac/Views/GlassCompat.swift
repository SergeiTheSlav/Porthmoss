import SwiftUI

/// Liquid Glass with a fallback for macOS 14 and 15.
///
/// Shared visual language with Termoss: the same shims, so the two apps look
/// like they came from the same place. Real Liquid Glass on macOS 26 and up, a
/// frosted material panel below it, deliberately not an imitation of Liquid
/// Glass, but what a native app of that era actually looked like.

@available(macOS 26.0, *)
private func liquidGlass(tint: Color?, interactive: Bool, enabled: Bool) -> Glass {
    guard enabled else { return .identity }
    var glass = Glass.regular
    if let tint { glass = glass.tint(tint) }
    if interactive { glass = glass.interactive() }
    return glass
}

extension View {
    /// A glass surface clipped to `shape`.
    ///
    /// - Parameters:
    ///   - enabled: When false this is a no-op, matching `.identity` on 26.
    ///     Used for hover states that only light up conditionally.
    ///   - tint: Colour wash over the surface.
    ///   - interactive: Liquid Glass's touch-reactive mode, ignored pre-26.
    @ViewBuilder
    func glassSurfaceEffect<S: InsettableShape>(
        in shape: S,
        enabled: Bool = true,
        tint: Color? = nil,
        interactive: Bool = false
    ) -> some View {
        if #available(macOS 26.0, *) {
            glassEffect(liquidGlass(tint: tint, interactive: interactive, enabled: enabled), in: shape)
        } else if enabled {
            background {
                ZStack {
                    shape.fill(.ultraThinMaterial)
                    if let tint { shape.fill(tint) }
                }
            }
            .overlay { shape.strokeBorder(Color.white.opacity(0.10), lineWidth: 0.5) }
        } else {
            self
        }
    }

    /// `.buttonStyle(.glass)` on 26, `.bordered` below it.
    @ViewBuilder
    func glassButtonStyle(prominent: Bool = false) -> some View {
        if #available(macOS 26.0, *) {
            if prominent { buttonStyle(.glassProminent) } else { buttonStyle(.glass) }
        } else {
            if prominent { buttonStyle(.borderedProminent) } else { buttonStyle(.bordered) }
        }
    }
}

/// Shared metrics, so the same control is never hand-rolled at three sizes.
enum Metrics {
    /// Corner radius of a panel.
    static let panelRadius: CGFloat = 16
    /// Corner radius of an inset row or field.
    static let rowRadius: CGFloat = 10
    /// Height of a pill, diameter of a circular icon button.
    static let control: CGFloat = 26
    /// Glyph size inside a control.
    static let glyph: CGFloat = 11
    /// Padding inside a panel.
    static let panelPadding: CGFloat = 16
    /// Gap between stacked panels.
    static let gap: CGFloat = 12
}

/// A rounded glass panel, the app's basic building block.
struct Panel<Content: View>: View {
    var tint: Color? = nil
    @ViewBuilder var content: Content

    var body: some View {
        content
            .padding(Metrics.panelPadding)
            .frame(maxWidth: .infinity, alignment: .leading)
            .glassSurfaceEffect(
                in: RoundedRectangle(cornerRadius: Metrics.panelRadius, style: .continuous),
                tint: tint
            )
    }
}

/// A circular icon button. Use this rather than hand-rolling
/// `Image → frame → background(Circle())` at the call site.
struct IconButton: View {
    let systemName: String
    var tint: Color? = nil
    var help: String = ""
    let action: () -> Void

    @State private var isHovering = false

    var body: some View {
        Button(action: action) {
            Image(systemName: systemName)
                .font(.system(size: Metrics.glyph, weight: .medium))
                .foregroundStyle(tint ?? Color.secondary)
                .frame(width: Metrics.control, height: Metrics.control)
                .glassSurfaceEffect(
                    in: Circle(),
                    tint: isHovering ? .white.opacity(0.10) : nil,
                    interactive: true
                )
                .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .onHover { isHovering = $0 }
        .help(help)
    }
}

/// A labelled row of the form "name ......... value".
struct DetailRow: View {
    let label: String
    let value: String
    var mono = false

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(label)
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            Spacer(minLength: 12)
            Text(value)
                .font(.system(size: 11, weight: .medium, design: mono ? .monospaced : .default))
                .foregroundStyle(.primary)
                .textSelection(.enabled)
                .multilineTextAlignment(.trailing)
        }
    }
}

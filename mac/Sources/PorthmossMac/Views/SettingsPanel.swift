import PorthmossCore
import SwiftUI

struct SettingsPanel: View {
    @EnvironmentObject private var model: AppModel
    @State private var expanded = false

    var body: some View {
        Panel {
            VStack(alignment: .leading, spacing: 12) {
                Button {
                    withAnimation(.snappy(duration: 0.22)) { expanded.toggle() }
                } label: {
                    HStack {
                        Text("Settings").font(.system(size: 12, weight: .semibold))
                        Spacer()
                        Image(systemName: "chevron.right")
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundStyle(.secondary)
                            .rotationEffect(.degrees(expanded ? 90 : 0))
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)

                if expanded {
                    VStack(alignment: .leading, spacing: 14) {
                        edgePicker
                        displayPicker
                        slider(
                            "Pointer speed on the PC",
                            value: $model.draft.capture.sensitivity,
                            range: 0.4 ... 2.5,
                            format: { String(format: "%.1f×", $0) }
                        )
                        slider(
                            "Push needed to cross",
                            value: $model.draft.capture.pushThreshold,
                            range: 4 ... 40,
                            format: { "\(Int($0)) pt" },
                            caption: "Higher means less chance of crossing by accident."
                        )

                        // The labels are given the full width so both switches
                        // line up on the trailing edge, rather than sitting
                        // wherever their own text happens to end.
                        Toggle(isOn: commandIsControl) {
                            VStack(alignment: .leading, spacing: 1) {
                                Text("⌘ acts as Ctrl on the PC").font(.system(size: 11))
                                Text("So ⌘C and ⌘T keep working. Mac Ctrl becomes the Windows key.")
                                    .font(.system(size: 10))
                                    .foregroundStyle(.secondary)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .toggleStyle(.switch)
                        .controlSize(.small)

                        Toggle(isOn: $model.draft.invertScroll) {
                            Text("Invert scrolling on the PC")
                                .font(.system(size: 11))
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .toggleStyle(.switch)
                        .controlSize(.small)

                        Divider().opacity(0.4)

                        HStack {
                            Text(model.hasUnappliedChanges
                                 ? "Unapplied changes"
                                 : "Up to date")
                                .font(.system(size: 10))
                                .foregroundStyle(model.hasUnappliedChanges ? .secondary : .tertiary)
                            Spacer()
                            Button("Revert") { model.discardSettingChanges() }
                                .glassButtonStyle()
                                .controlSize(.small)
                                .disabled(!model.hasUnappliedChanges)
                            Button("Apply") { model.applySettings() }
                                .glassButtonStyle(prominent: true)
                                .controlSize(.small)
                                .disabled(!model.hasUnappliedChanges)
                        }
                    }
                    .transition(.opacity.combined(with: .move(edge: .top)))
                }
            }
        }
    }

    private var edgePicker: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text("The PC is beyond this edge")
                .font(.system(size: 11))
            Picker("", selection: $model.draft.capture.edge) {
                ForEach(ScreenEdge.allCases, id: \.self) { edge in
                    Text(edge.rawValue.capitalized).tag(edge)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
        }
    }

    /// Which screen's edge leads to the PC.
    ///
    /// Only worth showing on a Mac with more than one display: with one
    /// screen there is nothing to choose, and the row would just be noise.
    @ViewBuilder
    private var displayPicker: some View {
        if model.displays.count > 1 {
            VStack(alignment: .leading, spacing: 5) {
                Text("…of this screen")
                    .font(.system(size: 11))
                Picker("", selection: $model.draft.crossingDisplay) {
                    Text("Whichever screen is on the outside").tag("")
                    ForEach(model.displays) { display in
                        Text(display.isMain ? "\(display.name) (main)" : display.name)
                            .tag(display.id)
                    }
                }
                .pickerStyle(.menu)
                .labelsHidden()
                if let note = displayNote {
                    Text(note)
                        .font(.system(size: 10))
                        .foregroundStyle(.tertiary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }

    /// The consequence of the current choice, in a line.
    ///
    /// Picking an edge that has another Mac display beyond it is a real
    /// trade, not a mistake — it is the only way to put the PC past the
    /// *inner* edge of two side-by-side screens — so it is explained rather
    /// than forbidden.
    private var displayNote: String? {
        guard !model.draft.crossingDisplay.isEmpty else {
            return "Any screen whose \(model.draft.capture.edge.rawValue) edge is free leads to the PC."
        }
        guard let display = model.chosenDisplay else {
            return "That screen is not connected. Until it is, the outside edge is used instead."
        }
        guard model.hasDisplayBeyond(display, edge: model.draft.capture.edge) else {
            return "Only \(display.name) leads to the PC. The other screens keep their own edges."
        }
        return """
        Another screen is beyond that edge. A firm push crosses to the PC; \
        nudge, pause, then nudge again to pass through to that screen instead.
        """
    }

    private func slider(
        _ title: String,
        value: Binding<Double>,
        range: ClosedRange<Double>,
        format: @escaping (Double) -> String,
        caption: String? = nil
    ) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack {
                Text(title).font(.system(size: 11))
                Spacer()
                Text(format(value.wrappedValue))
                    .font(.system(size: 11, weight: .medium, design: .monospaced))
                    .foregroundStyle(.secondary)
            }
            Slider(value: value, in: range).controlSize(.small)
            if let caption {
                Text(caption).font(.system(size: 10)).foregroundStyle(.tertiary)
            }
        }
    }

    /// The modifier mapping is a whole struct; the only choice worth exposing
    /// is which of the two presets is in play.
    private var commandIsControl: Binding<Bool> {
        Binding(
            get: { model.draft.modifiers.command.code == 0x1D },
            set: { model.draft.modifiers = $0 ? .default : .passthrough }
        )
    }
}

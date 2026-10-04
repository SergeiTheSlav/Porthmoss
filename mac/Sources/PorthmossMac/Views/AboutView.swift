import PorthmossCore
import SwiftUI

/// Who made this and what it is built on.
///
/// Reached by clicking the wordmark, and nowhere else. An open-source project
/// that does not say whose it is, what licence it carries, or whose work it
/// stands on is missing something, but nobody opens the app to read credits,
/// so this does not get a permanent row in a window that is otherwise all
/// things you came here to do.
struct AboutView: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(About.name)
                    .font(.system(size: 15, weight: .semibold))
                Text("Version \(About.version) · \(About.licence) licence")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }

            VStack(alignment: .leading, spacing: 6) {
                DetailRow(label: "Made by", value: About.author)
                DetailRow(label: "Copyright", value: About.copyright)
                HStack {
                    Text("Source")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 12)
                    Link(destination: URL(string: About.repository)!) {
                        Text(About.repository.replacingOccurrences(of: "https://", with: ""))
                            .font(.system(size: 11, weight: .medium))
                    }
                }
            }

            Divider().opacity(0.4)

            Text("Standing on")
                .font(.system(size: 11, weight: .medium))
            // Each one says what it actually does here. A bare list of module
            // paths is a licence notice, not a credit.
            VStack(alignment: .leading, spacing: 6) {
                ForEach(About.acknowledgements) { credit in
                    VStack(alignment: .leading, spacing: 0) {
                        Link(destination: URL(string: credit.url)!) {
                            Text(credit.name)
                                .font(.system(size: 11, weight: .medium))
                        }
                        Text("\(credit.role) · \(credit.licence)")
                            .font(.system(size: 10))
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
            }

            Text("The Mac app itself has no third-party dependencies; "
                + "these are what the Windows agent is built on.")
                .font(.system(size: 10))
                .foregroundStyle(.tertiary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(16)
        .frame(width: 300)
    }
}

#!/bin/bash
# Builds a disk image to hand to somebody else.
#
# Three things separate this from `make app`:
#
#   * Universal. The local build is whatever this Mac is; a build for someone
#     else has to run on Intel too.
#   * Ad-hoc signed. The local signing identity is a certificate that exists
#     only in this keychain, so elsewhere it names an authority the system has
#     never heard of — worse than claiming none.
#   * It carries the Windows agent. The Mac app on its own does nothing at all;
#     shipping it alone would be shipping half a product.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="$(cd "$ROOT/.." && pwd)"
# Read the version from the one place it is written down.
VERSION="$(sed -n 's/.*static let version = "\([^"]*\)".*/\1/p' \
    "$ROOT/Sources/PorthmossCore/About.swift" | head -1)"
[ -n "$VERSION" ] || { echo "could not read the version from About.swift" >&2; exit 1; }
DMG="$REPO/dist/Porthmoss-$VERSION.dmg"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

echo "Building the Mac app for Intel and Apple silicon…"
(cd "$ROOT" && swift build -c release --arch arm64 --arch x86_64 > /dev/null)

echo "Building the Windows agent…"
(cd "$REPO/win" && GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -H windowsgui" \
    -o "$STAGE/Windows agent/porthmoss-agent.exe" ./cmd/porthmoss-agent)
(cd "$REPO/win" && GOOS=windows GOARCH=arm64 go build -ldflags "-s -w -H windowsgui" \
    -o "$STAGE/Windows agent/porthmoss-agent-arm64.exe" ./cmd/porthmoss-agent)

PORTHMOSS_BINARY="$ROOT/.build/apple/Products/Release/PorthmossMac" \
PORTHMOSS_SIGN=adhoc \
APP_OUT="$STAGE/Porthmoss.app" \
    "$ROOT/Scripts/make-app.sh" release > /dev/null

# Drag-to-install.
ln -s /Applications "$STAGE/Applications"

cat > "$STAGE/Read me first.txt" <<'README'
Porthmoss
=========

Control a Windows PC from your Mac. Push the cursor past the edge of the
Mac's screen and your mouse and keyboard land on the PC instead — it keeps
its own display, running its own desktop. No video is streamed, so there is
no lag and no bandwidth cost.

Both machines must be on the same network.


1. Install the Mac app
----------------------

Drag Porthmoss to the Applications folder.

The first launch needs one extra step, because the app is not notarised by
Apple — that needs a paid developer account. Double-clicking will refuse.

  On macOS 15 (Sequoia) and later:
    Open it once and let it be blocked. Then go to
    System Settings > Privacy & Security, scroll down, and click
    "Open Anyway" next to the message about Porthmoss.

  On macOS 14 (Sonoma) and earlier:
    Right-click Porthmoss, choose Open, and confirm.

Either way, only the first time.


2. Give it permission
---------------------

Porthmoss needs BOTH of these in System Settings > Privacy & Security:

  * Accessibility      — to move your cursor and send keystrokes
  * Input Monitoring    — to see the keyboard at all

Both are required, and they are separate lists. With only Accessibility
granted, the mouse works and the keyboard silently does nothing.

If Porthmoss is already listed but still complains, select it, remove it
with the minus button, and add it again.


3. Set up the PC
----------------

Copy porthmoss-agent.exe from the "Windows agent" folder to the Windows
machine and run it. Use the -arm64 build only for an ARM Windows PC, which
is unusual.

Windows will warn that it is from an unknown publisher: choose More info,
then Run anyway. Allow it through the firewall on Private networks.

It puts an icon in the notification area and shows a six-digit code.


4. Pair them
------------

Open Porthmoss on the Mac. It finds the PC on the network by itself, then
asks for the six digits the PC is showing. That happens once per PC.


Using it
--------

Push the cursor firmly against the right edge of your Mac's screen — a
deliberate shove, not a brush — and it appears on the PC. Push back at the
far edge to come home, or press Control-Option-Command-P at any time.

Which edge leads to the PC is in Settings, along with pointer speed and how
hard the push has to be.

With two screens attached, Settings also asks WHICH screen's edge leads to
the PC. By default any edge with nothing of the Mac's own beyond it will do,
which is ambiguous as soon as two screens both have a free edge on the same
side — so pick one by name and there is exactly one way across.

  * Cmd becomes Ctrl on the PC, so Cmd-C and Cmd-T keep working.
  * Copy and paste crosses over, text and files both ways.
  * Drag files from the Mac onto the PC and they land in Downloads\Porthmoss.
  * The PC's own mouse keeps working the whole time; both move one cursor.

Windows will not let anything drive UAC prompts or the lock screen. That is
a Windows rule, not a missing feature.


About
-----

Porthmoss is free and open source, under the MIT licence.
Made by Jan Jamscikov. Source, issues and newer versions:

  https://github.com/SergeiTheSlav/Porthmoss

The About panel in the Mac app and the About card in the PC window list
everything this is built on, and under which licence.
README

echo "Making the disk image…"
rm -f "$DMG"
hdiutil create -volname "Porthmoss" -srcfolder "$STAGE" -ov -format UDZO "$DMG" > /dev/null

echo "Built $DMG"
ls -lh "$DMG"

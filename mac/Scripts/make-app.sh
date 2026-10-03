#!/bin/bash
# Builds Porthmoss.app.
#
# The bundle is not optional. macOS grants Accessibility and Input Monitoring
# to a code identity rather than to a path, so a bare binary inherits whatever
# the launching terminal was granted — which is both wrong and confusing. A
# bundle with a stable identifier gets its own entry in System Settings, and
# can be launched from Finder like anything else.
set -euo pipefail

CONFIG="${1:-release}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="$(cd "$ROOT/.." && pwd)"
# PORTHMOSS_BINARY lets the release script hand over a universal binary built
# with `swift build --arch arm64 --arch x86_64`, which SwiftPM puts somewhere
# else entirely.
BINARY="${PORTHMOSS_BINARY:-$ROOT/.build/$CONFIG/PorthmossMac}"
APP="${APP_OUT:-$REPO/dist/Porthmoss.app}"

[ -x "$BINARY" ] || { echo "build first: (cd mac && swift build -c $CONFIG)" >&2; exit 1; }

# The version is declared in the source, so the app, the About panel and the
# --version flag can never disagree about which build this is.
VERSION="$(sed -n 's/.*static let version = "\([^"]*\)".*/\1/p' \
    "$ROOT/Sources/PorthmossCore/About.swift" | head -1)"
[ -n "$VERSION" ] || { echo "could not read the version from About.swift" >&2; exit 1; }

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BINARY" "$APP/Contents/MacOS/Porthmoss"

# The icon is drawn from source rather than checked in as a binary, so it can
# be adjusted by editing the drawing instead of a pixel editor.
ICONSET="$(mktemp -d)/Porthmoss.iconset"
mkdir -p "$ICONSET"
ICONGEN="$(mktemp -d)/makeicon"
swiftc -swift-version 5 -O "$ROOT/Scripts/make-icon.swift" -o "$ICONGEN"
"$ICONGEN" "$ICONSET" > /dev/null
iconutil --convert icns --output "$APP/Contents/Resources/Porthmoss.icns" "$ICONSET"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>            <string>Porthmoss</string>
    <key>CFBundleDisplayName</key>     <string>Porthmoss</string>
    <key>CFBundleIdentifier</key>      <string>app.porthmoss.mac</string>
    <key>CFBundleExecutable</key>      <string>Porthmoss</string>
    <key>CFBundleIconFile</key>        <string>Porthmoss</string>
    <key>CFBundlePackageType</key>     <string>APPL</string>
    <key>CFBundleShortVersionString</key><string>$VERSION</string>
    <key>CFBundleVersion</key>         <string>1</string>
    <key>LSMinimumSystemVersion</key>  <string>14.0</string>
    <!-- A normal app: Dock icon, Cmd-Tab, and a Launchpad entry. It also
         puts an item in the menu bar, but LSUIElement would have cost the
         Dock icon and Cmd-Tab, which is a bad trade for a window you open. -->
    <key>LSUIElement</key>             <false/>
</dict>
</plist>
PLIST

# Sign with the local identity if it is available, and fall back to ad-hoc.
#
# This matters more than it looks. macOS grants Accessibility and Input
# Monitoring to a code identity, and an ad-hoc signature has none that is
# stable — its hash changes on every build, so each rebuild silently revoked
# both grants while System Settings still showed the switches as on.
KEYCHAIN="$HOME/Library/Keychains/porthmoss-signing.keychain-db"
# A build for somebody else is signed ad-hoc. The local identity is a
# certificate that exists only in this keychain, so on another Mac it names an
# authority the system has never heard of — worse than no authority at all.
if [ "${PORTHMOSS_SIGN:-local}" = "adhoc" ]; then
    codesign --force --sign - --timestamp=none "$APP"
    echo "Built $APP (ad-hoc signed, for distribution)"
    exit 0
fi
if IDENTITY="$("$ROOT/Scripts/signing-identity.sh" 2>/dev/null)" && [ -n "$IDENTITY" ]; then
    codesign --force --sign "$IDENTITY" --keychain "$KEYCHAIN" --timestamp=none "$APP"
else
    echo "warning: signing identity unavailable, falling back to ad-hoc;" >&2
    echo "         macOS will ask for permissions again after every build." >&2
    codesign --force --sign - --timestamp=none "$APP" 2>/dev/null
fi

# Finder caches icons aggressively and will happily show the old one forever.
touch "$APP"

echo "Built $APP"

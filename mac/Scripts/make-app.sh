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
BINARY="$ROOT/.build/$CONFIG/PorthmossMac"
APP="${APP_OUT:-$REPO/dist/Porthmoss.app}"

[ -x "$BINARY" ] || { echo "build first: (cd mac && swift build -c $CONFIG)" >&2; exit 1; }

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

cat > "$APP/Contents/Info.plist" <<'PLIST'
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
    <key>CFBundleShortVersionString</key><string>0.1.0</string>
    <key>CFBundleVersion</key>         <string>1</string>
    <key>LSMinimumSystemVersion</key>  <string>14.0</string>
    <!-- Menu bar app: no Dock icon. -->
    <key>LSUIElement</key>             <true/>
</dict>
</plist>
PLIST

# Ad-hoc signature: enough for a distinct TCC identity locally, but it changes
# on every rebuild, so macOS asks for the permissions again each time. A real
# Developer ID would stop that.
codesign --force --sign - --timestamp=none "$APP" 2>/dev/null

# Finder caches icons aggressively and will happily show the old one forever.
touch "$APP"

echo "Built $APP"

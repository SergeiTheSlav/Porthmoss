#!/bin/bash
# Wraps the built binary in a .app bundle.
#
# This is not cosmetic. macOS grants Accessibility and Input Monitoring to a
# *code identity*, not to a path — a bare CLI binary inherits whatever the
# terminal was granted, which is both confusing and wrong for a real install.
# A bundle with a stable identifier gets its own entry in System Settings.
set -euo pipefail

CONFIG="${1:-debug}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="$ROOT/.build/$CONFIG/PorthmossMac"
APP="$ROOT/.build/Porthmoss.app"

[ -x "$BINARY" ] || { echo "build first: swift build -c $CONFIG" >&2; exit 1; }

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"
cp "$BINARY" "$APP/Contents/MacOS/Porthmoss"

cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>            <string>Porthmoss</string>
    <key>CFBundleDisplayName</key>     <string>Porthmoss</string>
    <key>CFBundleIdentifier</key>      <string>app.porthmoss.mac</string>
    <key>CFBundleExecutable</key>      <string>Porthmoss</string>
    <key>CFBundlePackageType</key>     <string>APPL</string>
    <key>CFBundleShortVersionString</key><string>0.1.0</string>
    <key>CFBundleVersion</key>         <string>1</string>
    <key>LSMinimumSystemVersion</key>  <string>14.0</string>
    <!-- Background agent: no Dock icon, no menu bar of its own yet. -->
    <key>LSUIElement</key>             <true/>
</dict>
</plist>
PLIST

# Ad-hoc signature. Good enough to get a distinct TCC identity locally, but it
# changes on every rebuild, so macOS will ask for the permissions again. Swap in
# a Developer ID once there is one and that stops.
codesign --force --sign - --timestamp=none "$APP" 2>/dev/null

echo "Built $APP"
echo
echo "First run:"
echo "  open -a \"$APP\" --args --help    # or run the binary inside directly"
echo
echo "Then grant Accessibility AND Input Monitoring in"
echo "System Settings > Privacy & Security."

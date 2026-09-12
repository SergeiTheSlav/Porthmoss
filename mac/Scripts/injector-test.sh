#!/bin/bash
# Checks that input from the PC actually drives this Mac.
#
# The injector is the half of reverse control that can be verified here: the
# Windows capture side cannot be run from a Mac at all, so this at least pins
# down that what arrives is applied correctly.
#
# It moves the cursor for about a second and puts it back. Do not be typing.
set -uo pipefail

# Another Porthmoss holding an event tap makes this test flaky rather than
# wrong: two taps compete for the same events, and which one wins varies. Say
# so plainly instead of failing intermittently.
if pgrep -f "Porthmoss.app/Contents/MacOS/Porthmoss" > /dev/null 2>&1; then
    echo "Porthmoss.app is running. Quit it first — two event taps compete," >&2
    echo "and this test will pass or fail depending on which one wins." >&2
    exit 2
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$WORK/src"
cp "$ROOT"/Sources/PorthmossCore/*.swift \
   "$ROOT"/Sources/PorthmossMac/MacInjector.swift \
   "$ROOT"/Sources/PorthmossMac/CursorControl.swift \
   "$ROOT"/Scripts/injector-probe.swift "$WORK/src/"
mv "$WORK/src/injector-probe.swift" "$WORK/src/main.swift"

# Built as a single module, so the cross-module imports and access modifiers
# that SwiftPM needs would only get in the way.
sed -i '' 's/^import PorthmossCore$//; s/^public //; s/ public / /g' "$WORK/src"/*.swift

swiftc -swift-version 5 "$WORK/src"/*.swift -o "$WORK/run" || exit 2
"$WORK/run"

#!/bin/bash
# Checks that input from the PC actually drives this Mac.
#
# The injector is the half of reverse control that can be verified here: the
# Windows capture side cannot be run from a Mac at all, so this at least pins
# down that what arrives is applied correctly.
#
# It moves the cursor for about a second and puts it back. Do not be typing.
set -uo pipefail

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

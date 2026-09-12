#!/bin/bash
# Regression test for "the Mac cursor moves while the PC is being driven".
#
# Swallowing a mouse event in a CGEventTap does not hold the cursor still: the
# tap controls what applications receive, while the window server moves the
# pointer from the HID stream regardless. This drove the cursor across the
# screen in lockstep with the PC's, and only an end-to-end run catches it — the
# crossing model on its own looks perfectly correct while it happens.
#
# Brings up a real agent and a real Mac client on loopback, pairs them, crosses
# over, then moves the mouse and checks the Mac cursor did not follow.
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

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
PORT="${1:-47989}"
trap 'rm -rf "$WORK"; pkill -f "$WORK/agent" 2>/dev/null; pkill -f "PorthmossMac --cli" 2>/dev/null' EXIT

echo "Building…"
(cd "$ROOT/win" && go build -o "$WORK/agent" ./cmd/porthmoss-agent) || exit 2
(cd "$ROOT/mac" && swift build) || exit 2
swiftc -swift-version 5 -O "$ROOT/mac/Scripts/cursor-drift-probe.swift" -o "$WORK/probe" || exit 2

MAC="$ROOT/mac/.build/debug/PorthmossMac"
# Its own configuration, so the test pairs from scratch and — more importantly
# — cannot disturb the user's. It drives this Mac with synthetic input, and
# synthetic keystrokes land in whatever has focus.
export PORTHMOSS_CONFIG_DIR="$WORK/config"
mkdir -p "$PORTHMOSS_CONFIG_DIR"
printf '{"edge":"right","agentHost":"127.0.0.1"}' > "$PORTHMOSS_CONFIG_DIR/settings.json"
restore() { :; }

"$WORK/agent" --console --port "$PORT" --bind 127.0.0.1 --no-mdns --state "$WORK/state" > "$WORK/agent.log" 2>&1 &
sleep 1
mkfifo "$WORK/in"
"$MAC" --cli --host 127.0.0.1 --port "$PORT" < "$WORK/in" > "$WORK/client.log" 2>&1 &
exec 3>"$WORK/in"

for _ in $(seq 1 40); do
  CODE=$(grep -oE 'Pairing code: [0-9]{6}' "$WORK/agent.log" | grep -oE '[0-9]{6}')
  [ -n "$CODE" ] && break
  sleep 0.25
done
echo "$CODE" >&3
for _ in $(seq 1 20); do grep -q "Ready\." "$WORK/client.log" && break; sleep 0.25; done
if ! grep -q "Ready\." "$WORK/client.log"; then
  echo "FAILED to establish a session:"; cat "$WORK/client.log"; restore; exit 2
fi

"$WORK/probe"; RESULT=$?
exec 3>&-
restore
exit $RESULT

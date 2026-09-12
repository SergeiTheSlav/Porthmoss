# Both halves build from the Mac: the Windows agent is pure Go and needs no
# Windows toolchain, no cgo, and no Windows machine to compile on.

MAC_BIN := mac/.build/debug/PorthmossMac
DIST    := dist

.PHONY: all mac agent agent-arm64 test test-go test-mac app dist clean

all: mac agent

mac:
	cd mac && swift build

# The GUI needs the bundle: macOS grants Accessibility and Input Monitoring to
# a code identity, not a path, so run Porthmoss.app rather than the raw binary.
run: app
	open mac/.build/Porthmoss.app

app: mac
	cd mac && ./Scripts/make-app.sh debug

# -H windowsgui drops the console window: this is a tray app. `--console`
# reattaches to the launching terminal when output is actually wanted.
GUI_LDFLAGS := -s -w -H windowsgui

agent:
	cd win && GOOS=windows GOARCH=amd64 go build -ldflags "$(GUI_LDFLAGS)" -o ../$(DIST)/porthmoss-agent.exe ./cmd/porthmoss-agent

# Windows on ARM (Snapdragon X, Parallels on Apple silicon).
agent-arm64:
	cd win && GOOS=windows GOARCH=arm64 go build -ldflags "$(GUI_LDFLAGS)" -o ../$(DIST)/porthmoss-agent-arm64.exe ./cmd/porthmoss-agent

dist: agent agent-arm64
	@ls -lh $(DIST)

test: test-go test-mac

# End-to-end: does the Mac cursor stay put while the PC is being driven?
# Needs a window server and moves the cursor for a second, so it is not part
# of `make test`.
test-cursor:
	mac/Scripts/cursor-drift-test.sh

test-go:
	cd win && go test ./... && go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...

# Needs the full Xcode toolchain: swift-testing's macros are not in the
# Command Line Tools. See README, "Running the tests".
test-mac:
	cd mac && swift test

clean:
	rm -rf mac/.build $(DIST)

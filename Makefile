# Both halves build from the Mac: the Windows agent is pure Go and needs no
# Windows toolchain, no cgo, and no Windows machine to compile on.

DIST := dist
APP  := $(DIST)/Porthmoss.app

.PHONY: all mac app run install uninstall agent agent-arm64 test test-go test-mac test-cursor dist clean

# The app is the deliverable on the Mac side, so a bare `make` produces
# something double-clickable in Finder rather than a binary in .build.
all: app agent

mac:
	cd mac && swift build

app:
	cd mac && swift build -c release
	cd mac && ./Scripts/make-app.sh release

run: app
	open $(APP)

# Put it where Finder and Spotlight expect to find it.
install: app
	rm -rf /Applications/Porthmoss.app
	cp -R $(APP) /Applications/
	@echo "Installed to /Applications/Porthmoss.app"

uninstall:
	rm -rf /Applications/Porthmoss.app

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

# Does input from the PC actually drive this Mac? Also moves the cursor.
test-injector:
	mac/Scripts/injector-test.sh

# -unsafeptr is off for one documented case: the clipboard converts addresses
# returned by Win32 into pointers. See internal/clipboard/clipboard_windows.go.
test-go:
	cd win && go test ./... \
	  && go vet -unsafeptr=false ./... \
	  && GOOS=windows GOARCH=amd64 go vet -unsafeptr=false ./...

# Needs the full Xcode toolchain: swift-testing's macros are not in the
# Command Line Tools. See README, "Running the tests".
test-mac:
	cd mac && swift test

clean:
	rm -rf mac/.build $(DIST)

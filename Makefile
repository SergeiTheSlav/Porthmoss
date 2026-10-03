# Both halves build from the Mac: the Windows agent is pure Go and needs no
# Windows toolchain, no cgo, and no Windows machine to compile on.

DIST := dist
APP  := $(DIST)/Porthmoss.app

.PHONY: all mac app dmg run install uninstall agent agent-arm64 test test-go test-mac test-cursor check-version dist clean

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
# A disk image to hand to somebody else: universal, ad-hoc signed, and
# carrying the Windows agent, without which the Mac app does nothing.
dmg:
	mac/Scripts/make-dmg.sh

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

test: check-version test-go test-mac

# The two halves are one product, so they must not report different versions.
# Each side declares its own — the Mac app cannot import Go constants and the
# agent cannot import Swift ones — which makes drifting apart the default
# unless something checks.
check-version:
	@mac_v=$$(sed -n 's/.*static let version = "\([^"]*\)".*/\1/p' \
	    mac/Sources/PorthmossCore/About.swift | head -1); \
	 win_v=$$(sed -n 's/.*Version *= *"\([^"]*\)".*/\1/p' \
	    win/internal/about/about.go | head -1); \
	 if [ "$$mac_v" != "$$win_v" ]; then \
	   echo "version mismatch: Mac says $$mac_v, agent says $$win_v" >&2; \
	   echo "  mac/Sources/PorthmossCore/About.swift" >&2; \
	   echo "  win/internal/about/about.go" >&2; \
	   exit 1; \
	 fi; \
	 echo "version $$mac_v on both sides" 

# End-to-end: does the Mac cursor stay put while the PC is being driven?
# Needs a window server and moves the cursor for a second, so it is not part
# of `make test`.
test-cursor:
	mac/Scripts/cursor-drift-test.sh

# -unsafeptr is off for two documented cases, both turning an address Win32
# handed us into a pointer: the clipboard's GlobalAlloc memory (`at` in
# internal/clipboard/clipboard_windows.go) and the struct a hook procedure is
# given in its LPARAM (`hookData` in internal/capture/hook_windows.go). Neither
# address is in the Go heap, so there is nothing for the collector to move.
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

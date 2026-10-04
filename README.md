# Porthmoss

Control a Windows PC from a Mac. Push the cursor past the edge of the Mac's
screen and your mouse and keyboard land on the PC, which keeps its own display
running its own desktop.

A software KVM without the video. Nothing is streamed, so there is no video
latency and no bandwidth cost, just a few bytes per mouse movement.

Swift and SwiftUI on the Mac, Go on the PC. macOS 14+, Windows 10/11. MIT.

## Features

- **Edge crossing** with a push threshold, so reaching for a scrollbar does not
  fling you onto the other machine
- **Pick which screen** leads to the PC when the Mac has more than one display
- **Clipboard both ways**, text and files
- **Drag files from the Mac** onto the PC, where they land in
  `Downloads\Porthmoss`
- **Cmd becomes Ctrl** on the PC, so Cmd+C and Cmd+T keep working
- **Three ways back**: push at the far edge, press Ctrl+Option+Cmd+P, or let the
  2 second dead-man switch fire
- **Pinned TLS 1.3** with a 6 digit pairing code, one Mac per agent
- The PC's own mouse and keyboard keep working throughout

## Install

You need both halves. The Mac app alone does nothing.

Neither half is signed by an authority your OS trusts, because both
certificates cost money. Notarising a Mac app needs a paid Apple Developer
account, and signing an EXE needs a certificate authority. Both machines will
warn you once on first launch. The steps below cover it.

Both machines must be on the same network.

### 1. Get it

Download [the latest release](https://github.com/SergeiTheSlav/Porthmoss/releases/latest).
`Porthmoss-x.y.z.dmg` holds the Mac app and both Windows agents.

Or build it, which needs Xcode and Go 1.26+:

```bash
git clone https://github.com/SergeiTheSlav/Porthmoss.git
cd Porthmoss
make install    # Porthmoss.app into /Applications
make agent      # dist/porthmoss-agent.exe for the PC
```

### 2. Open it on the Mac

Drag Porthmoss to Applications, then open it once the long way, because macOS
refuses a plain double-click on an app it cannot verify:

- **macOS 15 and later**: double-click, let it be blocked, then open System
  Settings > Privacy & Security, scroll down, and click **Open Anyway**.
- **macOS 14**: right-click the app, choose **Open**, confirm.
- **Terminal, either version**:
  `xattr -dr com.apple.quarantine /Applications/Porthmoss.app`

Once only, whichever route you take.

### 3. Give it permission

System Settings > Privacy & Security. Porthmoss needs **both**:

- **Accessibility**, to move the cursor and send keystrokes
- **Input Monitoring**, to see the keyboard at all

Both are required and they are separate lists. With only Accessibility granted,
the mouse works and the keyboard silently does nothing. Porthmoss checks both
itself and names whichever one is missing.

If it is already listed and still complains, remove it with the minus button
and add it again.

### 4. Run the agent on the PC

Copy `porthmoss-agent.exe` over and run it. The `-arm64` build is only for ARM
Windows (Snapdragon X, Parallels on Apple silicon).

- SmartScreen will call the publisher unknown. Choose **More info**, then **Run
  anyway**.
- Allow it through the Windows Firewall on **private** networks. It listens on
  TCP 47654.
- Some antivirus products flag anything calling `SendInput`. That is the API
  this is built on, and the source is here if you want to check it first.

The agent puts an icon in the notification area and shows a 6 digit code. Its
window uses WebView2, which ships with Windows 11 and most Windows 10 installs.
Without it the agent runs headless, and `--console` shows what it is doing.

### 5. Pair

Open Porthmoss on the Mac. It finds the PC by itself and asks once for the 6
digits on the PC's screen. After that it reconnects on its own.

### 6. Cross over

Push the cursor firmly against the right edge of the Mac's screen, a deliberate
shove and not a brush, and it appears on the PC. Push back at the far edge to
come home, or press Ctrl+Option+Cmd+P at any time.

Settings holds the edge, the screen, pointer speed, and how hard the push has
to be.

## Two screens on the Mac

With more than one display, "push the right edge" stops being a single place.
Settings > *The PC is beyond this edge* > *...of this screen* says which one.

By default any screen with a **free** edge leads to the PC. An edge is free
when none of the Mac's own displays occupies the space beyond it. On a laptop
with an external screen stacked above it, both of the laptop's side edges are
free, and so are the external screen's, while the boundary between them stays
an ordinary boundary.

Naming one screen narrows that to exactly one way across, which matters as soon
as two screens both have a free edge on the same side.

You can also pick an edge that is **not** free, the inner boundary between two
side-by-side screens. Porthmoss then holds the cursor against that boundary
while the push adds up, because otherwise the pointer reaches the next screen
before a push can register. To get to that screen normally: nudge, pause, nudge
again, and the pointer passes through.

`PorthmossMac --cli --displays` lists this Mac's screens and their free edges.

## How it works

```
MacBook                                   Windows PC
┌──────────────────────────┐              ┌──────────────────────────┐
│ CGEventTap @ HID level   │              │ porthmoss-agent.exe      │
│  ├ detects edge crossing │   TLS 1.3    │  ├ SendInput, absolute   │
│  ├ swallows events while │◄────────────►│  ├ SendInput, scancodes  │
│  │   driving the PC      │  TCP_NODELAY │  ├ reports monitor rects │
│  ├ owns the PC's cursor  │              │  └ releases all on drop  │
│  └ maps Cmd to Ctrl      │              └──────────────────────────┘
└──────────────────────────┘
```

**The Mac** runs a `CGEventTap` at `kCGHIDEventTap`, the only macOS API that
can both observe and consume input. While control is on the PC the tap returns
`nil` for every event, the cursor is hidden, and
`CGAssociateMouseAndMouseCursorPosition(false)` decouples the physical mouse so
deltas keep arriving past the screen edge.

**The Windows agent** uses `SendInput`. No driver, no kernel code, no elevation
for ordinary desktop use. Mouse positions travel as absolute coordinates,
because relative deltas get run through Windows pointer acceleration a second
time and feel wrong. Keys travel as PS/2 set-1 scancodes, so the agent never
needs to know the Mac's keyboard layout.

**The link** is one TLS 1.3 connection with Nagle disabled. Input events are
about 16 bytes, which is sub-millisecond on a LAN.

Wire format and trust model: [docs/protocol.md](docs/protocol.md).

## Getting back

Three independent ways, because being stranded with no cursor on either machine
is the worst thing this app could do to you:

1. Push back against the far edge of the Windows desktop.
2. **Ctrl+Option+Cmd+P**, always released locally and never forwarded.
3. Automatically, if the agent goes quiet for 2 seconds. The agent independently
   releases every held key and button after 2 seconds of silence, so a dropped
   Wi-Fi link cannot leave a modifier stuck down on the PC.

## Security

An input channel to a PC is total control of that PC, so it is authenticated as
well as encrypted.

The agent generates a self-signed certificate on first run. The Mac pins its
SHA-256 fingerprint on first pairing and refuses to connect if it ever changes,
saying so plainly instead of throwing a generic TLS error. Pairing also
establishes a 32 byte shared secret derived from the 6 digit code, salted with
that certificate fingerprint, so the same code typed at a different machine
derives a different secret and cannot be replayed. The secret lives in a 0600
file. Five failed attempts rotate the code. One Mac per agent.

The pairing deliberately avoids the macOS Keychain. A keychain item's ACL binds
to the code identity that created it, and an ad-hoc signature changes on every
rebuild, so macOS treats each build as a new app and asks for your login
password before handing the secret back. The trade is that anything already
running as you can read the file, which buys that attacker nothing: a process
running as you can synthesise the input directly.

## Building

Both halves build from the Mac. The Windows agent is pure Go, so no Windows
machine or toolchain is needed to produce the `.exe`.

```bash
make            # Porthmoss.app + the Windows agent, into dist/
make install    # also copy the app to /Applications
make dist       # agent for Windows x64 and ARM64
make dmg        # universal, ad-hoc signed disk image with both halves
make test       # version check, Go tests, Swift tests
```

The version is declared in `mac/Sources/PorthmossCore/About.swift` and read from
there by the app bundle and the disk image. The agent keeps its own copy in
`win/internal/about/about.go`, and `make check-version` fails the build if the
two disagree.

`make app` signs with a self-signed identity created on first build and kept in
its own keychain (`mac/Scripts/signing-identity.sh`). macOS grants Accessibility
and Input Monitoring to a code identity, and an ad-hoc signature has none that
is stable, so every rebuild used to silently revoke both grants while System
Settings still showed the switches as on. The signing identity pins the
designated requirement to the certificate, so a grant made once survives later
builds. `make dmg` signs ad-hoc, because elsewhere that certificate names an
authority the system has never heard of.

To remove it:
`security delete-keychain ~/Library/Keychains/porthmoss-signing.keychain-db`

## Headless

```bash
PorthmossMac --cli --help          # options
PorthmossMac --cli --displays      # screens and their free edges
PorthmossMac --cli --display "Built-in"
PorthmossMac --cli --version
```

```
porthmoss-agent.exe --console -v   # output on the terminal
porthmoss-agent.exe --version
porthmoss-agent.exe --unpair
```

## When the agent misbehaves

The agent is a tray program with no console, so failures go to a log:

    %AppData%\Porthmoss\porthmoss.log

The previous run is kept as `porthmoss.log.1`, because when the agent dies and
is relaunched the crash is in the previous run. A crash writes a
recovered-panic line with a stack before anything else, and the first line of
every log says which version wrote it.

## Limits

- Control flows one way. The Mac drives the PC.
- **UAC prompts, the lock screen and Ctrl+Alt+Del cannot be driven.** Windows
  forbids injected input there. Reaching them needs a signed kernel HID driver,
  which means an EV certificate plus Microsoft attestation signing.
- **Elevated windows ignore input** from a non-elevated agent (UIPI). An
  elevated service or a signed `uiAccess=true` manifest fixes this.
- **Anti-cheat-protected games reject injected input**, flagged
  `LLMHF_INJECTED`.
- File dragging works Mac to PC only. Windows gives no way to observe a drag
  starting in another application, so the other direction uses copy and paste.
- The agent's window needs WebView2. Without it the agent runs headless.
- Neither half is signed by a trusted authority. See [Install](#install).

## Tests

```bash
make test         # everything below except test-cursor
make test-go      # protocol, pairing, dead-man switch, injection, transfers
make test-mac     # wire codec, key mapping, edge crossing, resisted edges
make test-cursor  # end to end: does the Mac cursor stay put while driving?
```

`make test-cursor` pairs a real agent and a real client over loopback, crosses
over, and checks the Mac cursor does not follow the mouse. It needs a window
server and moves the cursor for about a second, so it sits outside `make test`.
It exists because swallowing an event in a CGEventTap does **not** hold the
cursor still: the tap controls what applications receive, while the window
server moves the pointer from the HID stream regardless. The crossing model
looks perfectly correct the whole time it is happening.

`PORTHMOSS_TRACE=1` makes the Mac print every motion decision.

`make test-mac` needs full Xcode, because swift-testing's macros are missing
from the Command Line Tools. If `xcode-select -p` still points at
`/Library/Developer/CommandLineTools`, switch it and accept the licence in this
order:

```bash
sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
sudo xcodebuild -license accept
```

## Layout

```
docs/protocol.md      wire format and trust model
mac/Sources/
  PorthmossCore/      wire codec, key map, edge-crossing model, version
  PorthmossMac/       event tap, TLS client, cursor control, displays, CLI
  PorthmossMac/Views/ SwiftUI menu bar app
win/
  cmd/porthmoss-agent entrypoint and the state-to-UI presenter
  internal/about      version, licence, acknowledgements
  internal/proto      wire protocol, mirrors PorthmossCore
  internal/inject     SendInput, plus a recording fake for tests on any OS
  internal/server     TLS server, handshake, dispatch, dead-man switch
  internal/pairing    certificate identity, HKDF pairing, HMAC auth
  internal/discovery  mDNS advertisement
  internal/ui         tray icon, WebView2 window, console fallback
```

## Contributing

Issues and pull requests welcome. `make test` must pass. The edge-crossing
rules in `PorthmossCore` decide how this feels, and they are unit-testable on
purpose, so a change to crossing behaviour should arrive with a test that
describes it in a sentence.

## Licence

MIT. See [LICENSE](LICENSE). Copyright 2026 Jan Jamscikov.

The Mac app has no third-party dependencies. What the Windows agent is built
on, and under which licence, is in
[THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md). Both apps show the same list
when you click the wordmark.

## Name

**πορθμός**, a strait: the narrow water between two shores, and the crossing
over it. The wordmark is set **πορθμόςς**, doubling the final sigma so it ends
the way Porthmoss does.

# Porthmoss

Control a Windows PC from a MacBook. Push the cursor past the edge of the Mac's
screen and the mouse and keyboard land on the PC instead — it keeps its own
display, running its own desktop.

This is a software KVM minus the V: input redirection, not screen sharing.
Nothing is streamed, so there is no video latency and no bandwidth cost — just a
few bytes per mouse movement.

MIT licensed. Both halves build from a Mac with no paid developer account on
either side, which is worth saying plainly because it shapes the install: the
Mac app is not notarised and the Windows agent is not code-signed, so the first
launch on each machine takes one extra click. See [Installing](#installing).

## Status

The control path works end to end: pairing, edge crossing, mouse, keyboard,
scroll, and the safety releases. Both ends have a UI — a menu bar app on the
Mac, a notification-area icon and window on the PC.

Clipboard text and files are shared both ways, and files dragged from the Mac
land on the PC. Dragging *off* Windows is not possible from outside the
application that starts the drag, so that direction goes through copy and
paste instead.

Control runs one way: the Mac drives the PC. The PC's own mouse and keyboard
keep working the whole time, and both mice move the same cursor.

## Installing

You need both halves. The Mac app on its own does nothing at all.

### 1. Get the software

**Build it yourself** — the recommended route, and the only one that needs no
trust in a binary somebody else compiled. It needs Xcode (for the Mac app) and
Go 1.26 or later (for the Windows agent); the agent is pure Go with no cgo, so
no Windows machine or toolchain is involved.

```bash
git clone https://github.com/SergeiTheSlav/Porthmoss.git
cd Porthmoss
make install    # builds Porthmoss.app and copies it to /Applications
make agent      # builds dist/porthmoss-agent.exe for the PC
```

Building it on the Mac that will run it has a second benefit, explained under
[Building](#building): the app is signed with an identity created on first
build and kept in its own keychain, so the Accessibility and Input Monitoring
grants you make once survive every later rebuild.

**Or take a disk image.** `make dmg` produces `dist/Porthmoss-<version>.dmg`,
universal, ad-hoc signed, with the Windows agent inside it and a read-me of its
own. That is the thing to hand to somebody who is not going to build anything.

### 2. Let macOS open it

Porthmoss is not notarised — notarisation requires a paid Apple Developer
account — so macOS refuses the first launch of a copy that arrived from
somewhere else. This affects a downloaded disk image, not an app you just built
on the machine you are building on.

- **macOS 15 (Sequoia) and later.** Double-click it and let it be blocked. Open
  System Settings → Privacy & Security, scroll down to the message about
  Porthmoss, and click **Open Anyway**.
- **macOS 14 (Sonoma).** Right-click the app, choose **Open**, and confirm.
- **From a terminal, either version.** `xattr -dr com.apple.quarantine
  /Applications/Porthmoss.app` removes the download flag, after which it opens
  normally.

Only the first time, whichever route you take.

### 3. Grant the two permissions

The Mac app needs **both** of these in System Settings → Privacy & Security:

- **Accessibility** — to move the cursor and send keystrokes
- **Input Monitoring** — to see the keyboard at all

They are separate lists and both are required. With only Accessibility granted,
the mouse works and the keyboard silently does nothing, with no error anywhere
to explain it — which is why Porthmoss checks both itself and names the one
that is missing.

Porthmoss asks on first launch. If it is already listed and still complains,
select it, remove it with the minus button, and add it again.

Run `Porthmoss.app`, not the bare binary in `.build`. macOS grants these
permissions to a code identity rather than to a path, so a raw executable
inherits whatever your terminal was granted, which is both wrong and confusing.

### 4. Start the agent on the PC

Copy `porthmoss-agent.exe` to the Windows machine and run it. Use the
`-arm64` build only on an ARM Windows PC (Snapdragon X, or Parallels on Apple
silicon); on everything else use the plain one.

- Windows SmartScreen will say the publisher is unknown, because the agent is
  not code-signed — that needs a certificate authority, which costs money.
  Choose **More info**, then **Run anyway**.
- Allow it through the **Windows Firewall on private networks** when asked. It
  listens on TCP 47654.
- Some antivirus products are suspicious of anything that calls `SendInput`.
  That is the API this is built on, and there is no way around it; the source
  is right here if you want to check what it does with it.

It puts an icon in the notification area and opens a window showing a six-digit
pairing code. The window is WebView2, which ships with Windows 11 and most
Windows 10 installs; without it the agent still works, it just runs headless,
and `--console` is how to see what it is doing.

### 5. Pair them

Open Porthmoss on the Mac. It finds the PC on the network by itself, and asks
once for the six digits the PC is showing. After that it reconnects on its own,
and the code is never needed again.

Both machines must be on the same network, and the Mac must be able to reach
the PC on port 47654.

### 6. Cross over

Push the cursor firmly against the right edge of the Mac's screen — a
deliberate shove, not a brush — and it appears on the PC. Push back at the far
edge to come home, or press **⌃⌥⌘P** at any time.

Which edge leads to the PC, and on which screen, is in Settings. So are pointer
speed and how hard the push has to be.

## Two screens on the Mac

With more than one display attached, "push the right edge" stops being a single
place, and Porthmoss lets you say which one you mean. Settings → *The PC is
beyond this edge* → *…of this screen*.

By default any screen whose chosen edge is **free** leads to the PC. An edge is
free when none of the Mac's own displays occupies the space beyond it — so on
the usual arrangement of a laptop with an external screen stacked above it,
both the laptop's left and right edges are free, and so are the external
screen's, while the laptop's top edge and the external screen's bottom edge are
the boundary between them and stay an ordinary boundary.

Picking one screen by name narrows that to exactly one way across, which is
what you want as soon as two screens both have a free edge on the same side.

You can also pick an edge that is *not* free — the inner boundary between two
side-by-side screens — and that is a real trade rather than a mistake. There,
Porthmoss holds the cursor against the boundary while the push adds up, because
without that the pointer is already on the next screen before a push could
register. Getting to that screen the ordinary way still works: nudge, pause,
then nudge again, and the pointer passes through instead. The app says so under
the picker when you choose such an edge.

`PorthmossMac --cli --displays` lists what this Mac has, with each screen's
identifier and which of its edges are free.

## How it works

```
MacBook                                   Windows PC
┌──────────────────────────┐              ┌──────────────────────────┐
│ CGEventTap @ HID level   │              │ porthmoss-agent.exe      │
│  ├ detects edge crossing │   TLS 1.3    │  ├ SendInput, absolute   │
│  ├ swallows events while │◄────────────►│  ├ SendInput, scancodes  │
│  │   driving the PC      │  TCP_NODELAY │  ├ reports monitor rects │
│  ├ owns the PC's cursor  │              │  └ releases all on drop  │
│  ├ maps Cmd → Ctrl       │              │                          │
│  └ MacInjector applies   │              │  ├ WH_MOUSE_LL           │
│      input from the PC   │              │  ├ WH_KEYBOARD_LL        │
└──────────────────────────┘              │  ├ detects edge crossing │
                                          │  └ parks its own pointer │
                                          └──────────────────────────┘
```

Each machine is both a source and a target. The same seven messages travel in
whichever direction control is going, over the one connection the Mac opened.

**The Mac** runs a `CGEventTap` at `kCGHIDEventTap`. It is the only macOS API
that can both observe *and consume* input — `NSEvent` global monitors can watch
but never swallow, which is useless when the Mac's own cursor has to stay still.
While control is on the PC, the tap returns `nil` for every event, the cursor is
hidden, and `CGAssociateMouseAndMouseCursorPosition(false)` decouples the
physical mouse so deltas keep arriving past the screen edge.

**The Windows agent** uses `SendInput`. No driver, no kernel code, no signing
certificate, and no elevation for ordinary desktop use. Mouse positions are sent
**absolute** rather than relative, because relative deltas get run through
Windows' pointer acceleration a second time and feel wrong; the Mac owns the
cursor position and the agent just places it. Keys travel as PS/2 set-1
scancodes, so the agent never needs to know the Mac's keyboard layout.

**The link** is one TLS 1.3 connection with Nagle disabled. Input events are
~16 bytes; on a LAN this is sub-millisecond, and UDP would buy nothing until
that is measured to be false.

See [docs/protocol.md](docs/protocol.md) for the wire format.

## What makes it feel native

- **Cmd becomes Ctrl.** Cmd+C, Cmd+T, Cmd+W keep working from muscle memory.
  Mac Control takes over the Windows key, which plays the same "system" role.
  `--passthrough` turns this off.
- **Push, don't brush.** Crossing requires ~14 points of sustained push into the
  edge, and the accumulated push decays after 400 ms. Reaching for a scrollbar
  does not fling you onto the other machine.
- **You come back where you left.** The vertical position is preserved in both
  directions, proportionally.
- **The boundary between two Mac displays stays a boundary.** Only an edge with
  nothing of the Mac's own beyond it leads to Windows — measured against where
  the displays actually are, not against the bounding box of the desktop, so a
  laptop with a screen stacked above it keeps both of its side edges. See
  [Two screens on the Mac](#two-screens-on-the-mac).
- **Trackpad scrolling is accumulated** into wheel notches, so slow two-finger
  scrolling produces steady movement instead of nothing.

## Getting out

Three independent ways, because being stranded with no cursor on either machine
is the worst thing this app could do to you:

1. Push back against the far edge of the Windows desktop.
2. **Ctrl+Option+Cmd+P** — always released locally, never forwarded.
3. Automatically, if the agent stops answering for 2 seconds. The agent
   independently releases every held key and button after 2 seconds of silence,
   so a dropped Wi-Fi link cannot leave a modifier stuck down on the PC.

## Security

An input channel to a PC is total control of that PC, so it is authenticated,
not just encrypted.

The agent generates a self-signed certificate on first run. The Mac pins its
SHA-256 fingerprint on first pairing and refuses to connect if it ever changes —
with a message that says so plainly rather than a generic TLS error. Pairing
also establishes a 32-byte shared secret derived from a 6-digit code the agent
displays, salted with that certificate fingerprint, so the same code typed at a
different machine derives a different secret and cannot be replayed. The secret
lives in a 0600 file, alongside the agent's own. Failed pairing attempts rotate
the code after five tries. Only one Mac may control an agent at a time.

The pairing deliberately does **not** live in the macOS Keychain. A keychain
item's ACL is bound to the code identity that created it, and an ad-hoc
signature changes on every rebuild — so macOS treats each build as a new app
and asks for the login password before handing the secret back. Being prompted
for your password to reach a PC on your own LAN is absurd. The trade is that
anything already running as you can read the file, which buys that attacker
nothing: a process running as you can synthesise the input directly.

## Building

Both halves build from the Mac — the Windows agent is pure Go, so no Windows
machine or toolchain is needed to produce the `.exe`.

```bash
make            # Porthmoss.app + the Windows agent, into dist/
make install    # also copy the app to /Applications
make dist       # agent for Windows x64 and ARM64
make dmg        # universal, ad-hoc signed disk image with both halves
```

`make` produces `dist/Porthmoss.app`, which you can double-click straight from
Finder. `make install` puts it in `/Applications` so Spotlight finds it too.

The version is declared once, in `mac/Sources/PorthmossCore/About.swift`, and
read from there by the app bundle and the disk image. The agent carries its own
copy in `win/internal/about/about.go` — Swift cannot import Go constants and Go
cannot import Swift ones — and `make check-version`, which `make test` runs,
fails if the two ever disagree.

The app icon is drawn from source by `mac/Scripts/make-icon.swift` and built
into the bundle, so it is changed by editing the drawing rather than a binary
asset. It borrows Termoss's background — the same slate squircle, gradient and
lit top edge, sampled from its icon rather than eyeballed — so the two read as
a pair on the Dock.

### Why it is signed at all

`make app` signs with a self-signed identity created on first build and kept in
its own keychain (`mac/Scripts/signing-identity.sh`). That is not decoration:
an ad-hoc signature has no stable identity, so **every rebuild silently revoked
both permissions** while System Settings still showed the switches as on. The
two fail differently — Accessibility gates creating the tap, Input Monitoring
gates whether it ever sees a keystroke — so the usual symptom was a working
mouse and a dead keyboard, with no error at all. The signing identity pins the
designated requirement to the certificate instead of the binary, so a grant
made once survives every later build.

That identity is local to the machine that created it, which is exactly why
`make dmg` signs ad-hoc instead: elsewhere, a certificate the system has never
heard of is worse than claiming no authority at all.

To remove it: `security delete-keychain
~/Library/Keychains/porthmoss-signing.keychain-db`

## Headless use

Both sides keep a front end for SSH sessions and test harnesses, with no window
server and no tray involved.

```bash
PorthmossMac --cli --help        # options
PorthmossMac --cli --displays    # this Mac's screens and their free edges
PorthmossMac --cli --display "Built-in"   # which screen's edge leads to the PC
PorthmossMac --cli --version     # version, licence, and what it is built on
```

```
porthmoss-agent.exe --console -v   # run with output on the terminal
porthmoss-agent.exe --version      # version, licence, and what it is built on
porthmoss-agent.exe --unpair       # forget the paired Mac
```

The menu bar glyph on the Mac fills in while the PC is being driven, so it
always answers "where is my keyboard going right now?"

## When the agent misbehaves

The agent is a tray program with no console, so if it dies or refuses to do
something, the reason is in its log rather than on screen:

    %AppData%\Porthmoss\porthmoss.log

The previous run's log is kept alongside it as `porthmoss.log.1`, because when
the agent dies and is relaunched the crash is in the *previous* run. A crash
writes a recovered-panic line with a stack there before anything else, and the
first line of every log says which version wrote it.

## Known limitations

- Control flows one way: the Mac drives the PC, not the other way round. A
  reverse path was built and then removed — it never worked reliably, and
  while it was in place a stuck suspension could leave the PC's own mouse
  dead. One direction that works beats two that do not.

- **The Windows secure desktop is out of reach.** UAC prompts, the lock screen
  and Ctrl+Alt+Del cannot be driven by `SendInput` — that is a Windows design
  decision, not a bug here. Reaching them needs a signed kernel HID driver
  (EV certificate plus Microsoft attestation signing).
- **Elevated windows ignore input** from a non-elevated agent (UIPI). Running
  the agent as an elevated service or with a signed `uiAccess=true` manifest
  fixes this without a driver.
- **Anti-cheat-protected games reject injected input**, which is flagged
  `LLMHF_INJECTED`.
- Dragging files works from the Mac to the PC only. Windows gives no way to
  observe a drag starting in another application — the payload belongs to the
  source app — so the other direction goes through copy and paste instead.
- The Windows window needs the WebView2 runtime. Present on Windows 11 and
  most Windows 10 machines; without it the agent runs headless.
- Neither half is signed by an authority anyone's operating system trusts, for
  the plain reason that both certificates cost money. See
  [Installing](#installing) for what that means in practice.

## Running the tests

```bash
make test         # version check, agent tests, Mac tests
make test-go      # agent: protocol, pairing, dead-man switch, single
                  #        controller, injection, clipboard, file transfer
make test-mac     # Mac: wire codec, key mapping, edge crossing, resisted edges
make test-cursor  # end-to-end: does the Mac cursor stay put while driving the PC?
```

`make test-cursor` pairs a real agent and a real client over loopback, crosses
over, and checks the Mac cursor does not follow the mouse. It needs a window
server and moves the cursor for about a second, so it is not part of
`make test`. It exists because swallowing an event in a CGEventTap does **not**
hold the cursor still — the tap controls what applications receive, while the
window server moves the pointer from the HID stream regardless — and nothing
short of an end-to-end run catches that. The crossing model looks perfectly
correct the whole time it is happening.

`PORTHMOSS_TRACE=1` makes the Mac side print every motion decision, which is
how that was pinned down.

`make test-mac` needs the full Xcode toolchain — swift-testing's macros are not
included in the Command Line Tools. If `xcode-select -p` still points at
`/Library/Developer/CommandLineTools`, switch it and accept the licence once,
**in this order** (`xcodebuild` refuses to run until the active directory is
already Xcode):

```bash
sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
sudo xcodebuild -license accept
```

## Layout

```
LICENSE               MIT
THIRD-PARTY-NOTICES.md what the Windows agent is built on
docs/protocol.md      wire format and trust model
mac/Sources/
  PorthmossCore/      wire codec, key map, edge-crossing model, version and
                      credits — no frameworks, so the rules that decide how
                      this feels are unit-testable
  PorthmossMac/       event tap, pinned TLS client, cursor control, display
                      geometry, CLI
  PorthmossMac/Views/ SwiftUI menu bar app, sharing Termoss's glass language
win/
  cmd/porthmoss-agent entrypoint and the state-to-UI presenter
  internal/about      version, licence and acknowledgements
  internal/proto      wire protocol, mirrors PorthmossCore
  internal/inject     SendInput, plus a recording fake for tests on any OS
  internal/server     TLS server, handshake, dispatch, dead-man switch
  internal/pairing    certificate identity, HKDF pairing, HMAC auth
  internal/discovery  mDNS advertisement
  internal/ui         tray icon, WebView2 window, Fluent page, console fallback
```

## Contributing

Issues and pull requests are welcome. Two things to know before you start:

- `make test` must pass. The edge-crossing rules in `PorthmossCore` are what
  make this feel good or awful, and they are unit-testable on purpose — a
  change to how crossing behaves should arrive with a test that describes the
  behaviour in a sentence.
- Comments here explain *why*, not *what*. Most of them exist because something
  behaved surprisingly once; that is the kind worth adding.

## Licence and credits

Porthmoss is MIT licensed — see [LICENSE](LICENSE). © 2026 Jan Jamscikov.

The Mac app has no third-party dependencies. The Windows agent stands on
[energye/systray](https://github.com/energye/systray),
[jchv/go-webview2](https://github.com/jchv/go-webview2),
[jchv/go-winloader](https://github.com/jchv/go-winloader),
[libp2p/zeroconf](https://github.com/libp2p/zeroconf),
[miekg/dns](https://github.com/miekg/dns) and
[golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) —
see [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md). The same list is in the
About panel of both apps, with what each one actually does here.

## Name

**πορθμός** — a strait: the narrow water between two shores, and the crossing
over it. The wordmark in the app is set **πορθμόςς**, doubling the final sigma
so it ends the way Porthmoss does.

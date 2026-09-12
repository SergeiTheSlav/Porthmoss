# Porthmoss

Control a Windows PC from a MacBook. Push the cursor past the edge of the Mac's
screen and the mouse and keyboard land on the PC instead — it keeps its own
display, running its own desktop.

This is a software KVM minus the V: input redirection, not screen sharing.
Nothing is streamed, so there is no video latency and no bandwidth cost — just a
few bytes per mouse movement.

## Status

The control path works end to end: pairing, edge crossing, mouse, keyboard,
scroll, and the safety releases. Both ends have a UI — a menu bar app on the
Mac, a notification-area icon and window on the PC. Clipboard sync and
drag-and-drop are not built yet.

## How it works

```
MacBook (source)                          Windows PC (target)
┌──────────────────────────┐              ┌──────────────────────────┐
│ CGEventTap @ HID level   │              │ porthmoss-agent.exe      │
│  ├ detects edge crossing │   TLS 1.3    │  ├ SendInput, absolute   │
│  ├ swallows events while │◄────────────►│  ├ SendInput, scancodes  │
│  │   driving the PC      │  TCP_NODELAY │  ├ reports monitor rects │
│  ├ owns the PC's cursor  │              │  └ releases all on drop  │
│  └ maps Cmd → Ctrl       │              └──────────────────────────┘
└──────────────────────────┘
```

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
- **Inner display edges stay inner.** On a multi-display Mac only the outer rim
  of the whole desktop leads to Windows; the boundary between two Mac displays
  stays an ordinary boundary.
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
```

`make` produces `dist/Porthmoss.app`, which you can double-click straight from
Finder. `make install` puts it in `/Applications` so Spotlight finds it too.

The app icon is drawn from source by `mac/Scripts/make-icon.swift` and built
into the bundle, so it is changed by editing the drawing rather than a binary
asset. It borrows Termoss's background — the same slate squircle, gradient and
lit top edge, sampled from its icon rather than eyeballed — so the two read as
a pair on the Dock.

## Running

On the **Windows PC**, copy `dist/porthmoss-agent.exe` over and run it. It puts
an icon in the notification area and opens a window showing the pairing code.
Allow it through the Windows Firewall on private networks when prompted.

The window is WebView2, which ships with Windows 11 and most Windows 10
installs. Without it the agent still works — it just runs headless, and
`--console` is the way to see what it is doing.

On the **Mac**, double-click `dist/Porthmoss.app` (or `make run`). It lives in
the menu bar, finds the PC on the network, and asks once for the 6-digit code
the agent is showing. After that it reconnects on its own.

The menu bar glyph fills in while the PC is being driven, so it always answers
"where is my keyboard going right now?"

Both sides keep a headless mode for SSH sessions and test harnesses:
`PorthmossMac --cli` and `porthmoss-agent.exe --console`.

### Permissions

The Mac app needs **both** Accessibility and Input Monitoring in
System Settings → Privacy & Security. Accessibility to modify events, Input
Monitoring to see keystrokes at all; with only one, the tap fails to install.

Run `Porthmoss.app`, not the bare binary — which is why `make` builds the
bundle rather than leaving you an executable. macOS grants those permissions to
a code identity rather than to a path, so a raw binary inherits whatever the
terminal was granted.

`make app` signs with a self-signed identity created on first build, kept in
its own keychain (`mac/Scripts/signing-identity.sh`). That is not decoration:
an ad-hoc signature has no stable identity, so **every rebuild silently
revoked both permissions** while System Settings still showed the switches as
on. The two fail differently — Accessibility gates creating the tap, Input
Monitoring gates whether it ever sees a keystroke — so the usual symptom was a
working mouse and a dead keyboard, with no error at all. The signing identity
pins the designated requirement to the certificate instead of the binary, so a
grant made once survives every later build.

To remove it: `security delete-keychain ~/Library/Keychains/porthmoss-signing.keychain-db`

## Known limitations

- **The Windows secure desktop is out of reach.** UAC prompts, the lock screen
  and Ctrl+Alt+Del cannot be driven by `SendInput` — that is a Windows design
  decision, not a bug here. Reaching them needs a signed kernel HID driver
  (EV certificate plus Microsoft attestation signing).
- **Elevated windows ignore input** from a non-elevated agent (UIPI). Running
  the agent as an elevated service or with a signed `uiAccess=true` manifest
  fixes this without a driver.
- **Anti-cheat-protected games reject injected input**, which is flagged
  `LLMHF_INJECTED`.
- Input only flows Mac to PC. The PC's own mouse and keyboard cannot drive
  the Mac; that needs low-level hooks on Windows and an injector on the Mac.
- Dragging files works from the Mac to the PC only. Windows gives no way to
  observe a drag starting in another application — the payload belongs to the
  source app — so the other direction goes through copy and paste instead.
- The Windows window needs the WebView2 runtime. Present on Windows 11 and
  most Windows 10 machines; without it the agent runs headless.

## Running the tests

```bash
make test-go      # agent: protocol, pairing, dead-man switch, single controller
make test-mac     # Mac: wire codec, key mapping, edge crossing
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
docs/protocol.md      wire format and trust model
mac/Sources/
  PorthmossCore/      wire codec, key map, edge-crossing model — no frameworks,
                      so the rules that decide how this feels are unit-testable
  PorthmossMac/       event tap, pinned TLS client, cursor control, CLI
  PorthmossMac/Views/ SwiftUI menu bar app, sharing Termoss's glass language
win/
  cmd/porthmoss-agent entrypoint and the state-to-UI presenter
  internal/proto      wire protocol, mirrors PorthmossCore
  internal/inject     SendInput, plus a recording fake for tests on any OS
  internal/server     TLS server, handshake, dispatch, dead-man switch
  internal/pairing    certificate identity, HKDF pairing, HMAC auth
  internal/discovery  mDNS advertisement
  internal/ui         tray icon, WebView2 window, Fluent page, console fallback
```

## Name

**πορθμός** — a strait: the narrow water between two shores, and the crossing
over it. The wordmark in the app is set **πορθμόςς**, doubling the final sigma
so it ends the way Porthmoss does.

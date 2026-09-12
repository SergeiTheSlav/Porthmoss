# Building reverse control: the PC driving the Mac

This is a handoff for Claude Code running **on Windows**. The Mac half is
built, tested and merged; what is missing is the Windows half, which cannot be
written or run from a Mac at all.

Read `docs/protocol.md` first — it is short, and everything below assumes it.

## What the product is

Porthmoss lets one machine's keyboard and mouse drive another over the LAN. It
is input redirection, not screen sharing: no video, a few bytes per mouse
movement. Today input flows **Mac → PC**. Your job is **PC → Mac**.

## What already exists

```
mac/Sources/PorthmossCore/   wire codec, key map, edge-crossing model
mac/Sources/PorthmossMac/    event tap, TLS client, SwiftUI app, MacInjector
win/internal/proto/          wire protocol (this is the shared contract)
win/internal/inject/         SendInput — applies the Mac's input to Windows
win/internal/server/         TLS server, handshake, dispatch, clipboard, files
win/internal/clipboard/      CF_HDROP and CF_UNICODETEXT
win/internal/transfer/       file send and receive
win/internal/ui/             tray icon and WebView2 window
win/internal/discovery/      mDNS
```

The Mac side is **already able to be a target**. `MacInjector` applies
`MOUSE_MOVE`, `MOUSE_BUTTON`, `MOUSE_WHEEL`, `KEY`, `KEY_RESET`, `ENTER` and
`LEAVE` arriving from the PC, and `Session.applyRemoteInput` routes them. It is
verified by `make test-injector`, which really does move the Mac's cursor.

**So the Mac needs no changes.** Send it the same messages it already sends,
in the other direction, over the same connection.

## What to build

A capture path on Windows, mirroring `mac/Sources/PorthmossMac/EventTap.swift`
and `CaptureModel.swift`:

1. **Capture and swallow input.** `SetWindowsHookExW` with `WH_MOUSE_LL` and
   `WH_KEYBOARD_LL`. Return 1 from the hook procedure instead of calling
   `CallNextHookEx` to swallow an event, exactly as the Mac's tap returns NULL.
2. **Detect the edge crossing.** Port the rules from
   `mac/Sources/PorthmossCore/CaptureModel.swift`. Do not reinvent them: the
   push threshold, the decay window, and the arming distance each exist because
   of a specific failure, and the comments say which.
3. **Park the pointer.** `ClipCursor` to a 1×1 rectangle, and hide it. This is
   the equivalent of `CursorControl.pin()` on the Mac, and it is the part that
   will bite you — see below.
4. **Send the messages.** They already exist in `win/internal/proto`. The
   server has a `send` function per session; reuse it.
5. **Release properly.** Push-back at the far edge, a panic hotkey, and the
   dead-man switch. On any of them send `LEAVE`, which makes the Mac release
   everything it is holding.

## Constraints that are not negotiable

- **Pure Go, no cgo.** The whole point is that `GOOS=windows go build` produces
  the `.exe` from a Mac. Use `golang.org/x/sys/windows` and `syscall.NewCallback`.
  `internal/inject/inject_windows.go` shows the house style for Win32 calls.
- **Keep `internal/proto` byte-compatible.** Two implementations read it. If
  you add a field, append it and make the decoder tolerate its absence —
  `proto.Hello.Flags` and `proto.FileBegin.Flags` are both done that way.
- **Threading.** `systray` locks the main OS thread and owns the main message
  loop; the WebView2 window has its own locked thread. A low-level hook needs a
  thread with a message loop of its own — give it one, and do not try to share
  either of the existing two. `internal/ui/windows.go` explains why.
- **`make test-go` must stay green,** and `GOOS=windows GOARCH=amd64 go vet`
  must pass. Both run from the Mac, so they are the only check on your code
  that the other side of this project can run.

## Things that already cost a day, so do not rediscover them

- **`SendInput` delivers keystrokes to the foreground window.** When the agent's
  own window was in front, every key the Mac sent vanished into it while the
  mouse kept working. The mouse/keyboard asymmetry is the signature of a focus
  problem, not a keyboard problem. The window now hides itself when a Mac takes
  control; your capture path will need the same care in reverse.
- **A low-level hook that is slow gets silently removed by Windows,** the way a
  slow `CGEventTap` does on macOS. Do no I/O in the hook procedure. Post to a
  channel and let another goroutine write to the socket.
- **Swallowing the event is not enough to stop the pointer.** On macOS,
  swallowing a mouse event in the tap does *not* hold the cursor still — the
  window server moves it from the HID stream regardless, and it had to be
  warped back on every event. Assume Windows has an equivalent trap and test
  for it explicitly: move the mouse a long way and check the pointer has not
  drifted, rather than assuming.
- **Do not let injected input feed back.** The Mac stamps its injected events
  with `MacInjector.injectedMarker` in the event's user data and ignores them
  in its own tap. `LLMHF_INJECTED` in the hook struct is the Windows
  equivalent — check it, or the two machines will drive each other.

## Protocol, in the direction you need

Identical to `docs/protocol.md`, sent PC → Mac on the same TLS connection:

    0x30 ENTER         u16 x, u16 y     the PC's cursor crossed onto the Mac
    0x10 MOUSE_MOVE    u16 x, u16 y     absolute, 0..65535 over the Mac desktop
    0x11 MOUSE_BUTTON  u8 button, u8 down
    0x12 MOUSE_WHEEL   i16 dx, i16 dy   in wheel notches
    0x20 KEY           u16 scancode, u8 down, u8 flags   PS/2 set 1, bit0 = E0
    0x21 KEY_RESET     -                release everything held
    0x31 LEAVE         -                control returns to the PC

Send **PS/2 set-1 scancodes**, not virtual key codes — the Mac maps them back
itself, and applies the modifier translation (the PC's Ctrl becomes ⌘, so
Ctrl+C works). `KeyMap.modifier(forScancode:extended:)` is that mapping.

Coordinates are normalised over the Mac's whole desktop, which it reports as
`ScreenInfo` in `READY`. The Mac does the same for the PC.

## How to know it works

```
make test-go        # protocol, pairing, dead-man switch, clipboard, transfers
make dist           # cross-compiles the .exe
```

On the Mac side, `make test-injector` proves the receiving half. Once your
capture path sends anything, that is the half already known to be good — so a
failure after that is yours.

Start by sending a single hard-coded `ENTER` followed by a few `MOUSE_MOVE`
messages on a timer, with no hooks at all, and watch the Mac's cursor move.
That separates "can I capture input" from "can I send it", and the second one
is already answered.

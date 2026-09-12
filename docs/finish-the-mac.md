# Finishing the Mac half of reverse control

> **Done.** Every item here was acted on in "Finish the Mac half of reverse
> control". Kept as the record of why those changes happened, and because the
> two questions at the end are still open — they need both machines.
>
> Item 2 was worse than described: `isControllingPC` was set from
> `hasPrefix("Controlling")`, so *every other* message cleared it, and a file
> transfer reporting "Sending…" flipped the menu bar mid-session.

This is a handoff for Claude Code running **on the Mac**. The Windows capture
half is written: the PC can now push its own cursor past the edge of its
desktop and drive the Mac. What is left is on your side, and it is smaller than
it sounds — the receiving path already works. What is missing is what happens
when that path goes *wrong*.

Read `docs/reverse-control-windows.md` (the Status section at the top) and
`docs/protocol.md` first. Both are short.

## What changed on the PC, so you know the contract

Nothing in `internal/proto` changed, and nothing on the Mac had to. The agent
sends the same seven messages the Mac already sends, in the other direction,
down the same connection the Mac opened:

    0x30 ENTER   0x10 MOUSE_MOVE   0x11 MOUSE_BUTTON   0x12 MOUSE_WHEEL
    0x20 KEY     0x21 KEY_RESET    0x31 LEAVE

New on the PC: `win/internal/capture` (the crossing model, ported from
`PorthmossCore/CaptureModel.swift`, plus the low-level hooks and pointer
parking), and two hooks on the server — `OnController`, which hands the capture
path a sender for the life of a session, and `OnRemoteControl`, which reports
the Mac taking the PC over so the PC stands down.

Two things the agent does *not* know, both deliberate and both documented in
the README's "Known limitations": it never learns the Mac's desktop size
(`READY` only travels PC→Mac, so it assumes the Mac's desktop matches its own
and `--sensitivity` covers the rest), and it parks its pointer rather than
hiding it.

The PC's escape hotkey is **Ctrl+Alt+Win+P**. Its `--edge` is the *opposite* of
the Mac's: each machine names the edge the other one lies beyond.

## What the Mac still needs

Ranked by what breaks if you skip it.

### 1. The dead-man switch only covers one direction

`Session.swift:140`, in the heartbeat:

```swift
if self.model.isRemote, Date().timeIntervalSince(self.lastPongAt) > 2.0 {
    self.panic("agent stopped responding")
}
```

`model.isRemote` means *this Mac is driving the PC*. When the PC is driving the
Mac, that is false, so the check never fires and the Mac notices nothing.

The failure: the PC is holding ⌘ down, the Wi-Fi drops without a TCP reset, and
the Mac keeps ⌘ held until the socket finally errors — which can be minutes.
Every keystroke the user makes in the meantime does the wrong thing. This is
exactly the case `MacInjector.releaseAll()` calls out ("a modifier stuck down
is worse than a lost keystroke, because every subsequent key does the wrong
thing"), and exactly what `docs/protocol.md` promises symmetrically: *losing
Wi-Fi must never leave the user with no cursor on either machine.*

There is a second half to it. While `isControlledByPC` is true,
`Session.swift:167` returns early from `handle`, so the Mac's own edge
detection is switched off. Until something clears that flag, the user cannot
push across to the PC any more either.

The fix is to make the heartbeat fire on both, releasing the injector and
clearing `isControlledByPC` when it is the receiving side that has gone quiet.
The agent already does the mirror of this: it drops a session after 2 s of
silence and releases everything held.

### 2. The recovery that does work is a string match

`AppModel.swift:288`:

```swift
private func handleSessionStatus(_ line: String) {
    statusLine = line
    isControllingPC = line.hasPrefix("Controlling")
    if line.hasPrefix("Connection lost") {
```

This is load-bearing, and not obviously so. On a *hard* socket error the chain
is `onDisconnect` → `Session.panic` → `onStatus("Connection lost: …")` →
`hasPrefix` → `teardown()` → `Session.stop()`, and `stop()` is the only thing
that calls `injector.releaseAll()` and clears `isControlledByPC`
(`Session.swift:114`). So today the Mac recovers from a dropped PC entirely by
matching the prefix of a human-readable sentence.

Rewording that string breaks the release path silently. Worth replacing with a
typed status — an enum with a `.connectionLost` case and a
`.controlling(Direction)` case — before adding a third caller to it, which
item 3 will.

### 3. The UI has no state for being driven

`AppModel.isControllingPC` (`AppModel.swift:75`) is Mac→PC only. There is no
mirror. When the PC takes the Mac over, `MainWindow.swift:118` still renders
"Connected to \(host)" with a green tick, and only the status line underneath
says otherwise.

The Windows agent already carries both: `ui.State.Controlled` ("a Mac is
driving this PC") and `ui.State.Capturing` ("this PC is driving the Mac"). The
Mac wants the same pair, and the same distinction in the menu bar
(`MenuBarContent.swift:43`).

### 4. A decision, not a bug: the Mac does not swallow while being driven

`Session.swift:167` returns `false` when `isControlledByPC`, so the event is
passed through and the user's own trackpad still moves the Mac's cursor —
fighting the position the PC is injecting.

Do not "fix" this without deciding it deliberately, because the two ends are
currently symmetric: the PC does not swallow local input while the Mac drives
it either (`Controller.Suspend` stops the PC *capturing*, it does not consume
anything). Whichever way you go, both ends should agree, and the reason should
be written down next to the code.

### 5. The README's Status section is stale

It says "Clipboard sync and drag-and-drop are not built yet." Both are built,
on both ends: `ClipboardBridge.swift` and `FileTransfer.swift` here,
`win/internal/clipboard` and `win/internal/transfer` there, with the receiver's
name sanitisation tested. I did not touch that sentence because it predates
this work and I could not verify the feature end to end.

## What has actually been verified, and what has not

On a Windows 11 machine with Go 1.27 and no Mac present:

- `go build`, `go vet -unsafeptr=false` and `go test ./...` pass — natively,
  and cross-compiled for windows/amd64, windows/arm64 and darwin/arm64.
- `gofmt` is clean; both release binaries link.
- `TestHookInstallsAndComesBackDown` installs the two low-level hooks and takes
  them down without wedging, which also proves the `MSLLHOOKSTRUCT` and
  `KBDLLHOOKSTRUCT` size assertions against a real toolchain.
- The agent runs and logs `capturing this PC's mouse and keyboard`.

One pre-existing bug was fixed on the way: `TestSafeNameCannotEscape` in
`internal/transfer` asserted `strings.HasPrefix(joined, "/downloads/")`, which
can never hold on Windows because `filepath.Join` yields `\downloads\…`. The
sanitisation itself was correct; the test had simply never been run on the
platform it defends. It now compares with `filepath.Dir`.

**Nothing has been driven end to end.** No Mac was attached to any of it. The
crossing, the swallowing and the pointer parking are all unwitnessed.

## The two questions only the pair of machines can answer

Both are in `docs/reverse-control-windows.md` in more detail.

1. **Does Windows keep delivering mouse events once the pointer is pinned
   against the edge of the desktop?** The pointer stops dead there, so a second
   shove may report no movement at all — and the ported rules need movement at
   the edge to accumulate a push. `Controller.moveWhileLocalLocked` pulls the
   pointer `edgeNudge` (6 px) clear when an event arrives carrying no movement,
   which works whichever way the answer goes, but only one of those two paths
   is the one that really runs. Symptom to watch for: a crossing that takes a
   single fast approach rather than a deliberate shove, or one that never
   triggers however hard you push.

2. **Does swallowing the event hold the pointer still?** It does not on macOS.
   The Windows side assumes it does not there either and warps the pointer back
   to an anchor after every event. Test it the way the original brief says —
   move the mouse a long way and check the pointer has not drifted — rather
   than assuming either answer.

## How to try it

On the PC, prove the link before trusting the hooks:

```
porthmoss-agent.exe --console -v --probe-mac
```

That installs no hooks. As soon as a Mac connects it sweeps the Mac's cursor
across the screen and logs what happened. Since `make test-injector` already
proves the receiving half, a sweep that works means everything from the PC to
`MacInjector` is good and any remaining fault is in the capture path.

Then for real, with `--edge` set to whichever edge of the PC's desktop your Mac
sits beyond:

```
porthmoss-agent.exe --console -v --edge left
```

## Checks that must stay green

```
make test-mac     # needs the full Xcode toolchain, see README
make test-go      # runs from either machine
make test-injector
make test-cursor
```

`make test-go` now also covers the PC's crossing rules and its capture path.
`internal/capture/model_test.go` is `CaptureModelTests.swift` ported case for
case, sign-flipped — if you change a crossing rule on the Mac, that file is
where the Windows side will disagree with you, and it should be changed in the
same commit.

Keep `internal/proto` byte-compatible. Two implementations read it; append
fields and make the decoder tolerate their absence, the way `proto.Hello.Flags`
and `proto.FileBegin.Flags` already do.

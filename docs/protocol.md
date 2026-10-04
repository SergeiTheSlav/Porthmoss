# Porthmoss wire protocol v1

Transport: a single TLS 1.3 TCP connection, `TCP_NODELAY` set on both ends.
The **Windows agent is the TLS server**; the **Mac is the client**. The Mac
discovers agents over mDNS (`_porthmoss._tcp`) or is pointed at a host:port.

Byte order is **big-endian** throughout. Strings are `u16` length + UTF-8 bytes.

## Framing

Every message is:

    u32 length   (byte count of type + body, max 4096)
    u8  type
    ... body

## Trust model

The agent generates a self-signed P-256 certificate on first run and stores it
under its state dir. The Mac pins the certificate's SHA-256 fingerprint on
first pairing (TOFU) and refuses to connect if it ever changes.

Pinning alone proves *which* machine you reached, not that the user authorised
it, so pairing also establishes a 32-byte shared secret. The agent displays a
6-digit code; the Mac derives

    secret = HKDF-SHA256(ikm = code, salt = server_cert_fingerprint, info = "porthmoss-v1-pairing")

and proves knowledge of it with an HMAC over a server nonce. The secret is
stored in the macOS Keychain and in the agent's state file; the code is used
once and discarded.

## Handshake

    Mac  -> Agent   0x01 HELLO      u16 protocol_version, str client_name
    Mac  <- Agent   0x02 CHALLENGE  [32]byte nonce, u8 needs_pairing
    Mac  -> Agent   0x03 AUTH       [32]byte hmac_sha256(secret, nonce)
    Mac  <- Agent   0x04 READY      ScreenInfo

`needs_pairing = 1` means the agent has no stored secret and is showing a code.
Any auth failure is answered with `0x05 ERROR` and the connection is closed.

### ScreenInfo

    i32 virtual_left, virtual_top, virtual_width, virtual_height
    u8  monitor_count
    monitor_count x { i32 left, top, width, height; u8 is_primary }

Coordinates are Windows virtual-desktop pixels. The Mac uses the virtual rect
to convert its own cursor model into the normalised 0..65535 space below.

There is no message the other way: the Mac never describes itself, because
nothing on the PC needs to know. Control only flows one way.

## Input messages (Mac -> Agent)

All positions are **absolute**, normalised to the PC's virtual desktop:
`n = round(65535 * (px - virtual_left) / (virtual_width - 1))`.

Absolute is deliberate: relative deltas get run through Windows' pointer
acceleration a second time and feel wrong. The Mac owns the cursor position.

    0x10 MOUSE_MOVE    u16 x, u16 y
    0x11 MOUSE_BUTTON  u8 button (1=L 2=R 3=M 4=X1 5=X2), u8 down
    0x12 MOUSE_WHEEL   i16 dx, i16 dy      (units of WHEEL_DELTA/120)
    0x20 KEY           u16 scancode, u8 down, u8 flags (bit0 = extended)
    0x21 KEY_RESET     -                   (release every key the agent holds)

Key events carry **PS/2 set-1 scancodes**, not virtual key codes, so the agent
never has to know the Mac's keyboard layout. The Mac is responsible for
Cmd->Ctrl and other modifier remapping before it gets here.

`ENTER` places the pointer at exactly the position it carries. `MOUSE_MOVE`
does **not**: the agent applies the *difference* since the previous message to
wherever the pointer actually is. The PC's own mouse stays live throughout, and
an absolute placement would undo whatever the user's hand just did, they nudge
it, the next message snaps it back, and the cursor visibly teleports. Taking
the difference means both mice move one cursor.

## Clipboard

    0x50 CLIPBOARD_TEXT  utf-8 bytes

Both ends poll their own clipboard and send changes. The hard part is not
copying text but not echoing it: writing what the other machine sent changes
the local clipboard, the watcher notices, and the two bounce it between them.
Each side guards with both the clipboard's change counter and the last text it
wrote.

## File transfer

One file at a time, in order. Not interleaved: two at once would
need stream ids and a scheduler, to save a user dragging a folder half a second.

    0x60 FILE_BEGIN  u16 name_len, name, u64 size, u16 index, u16 total, u8 flags
    0x61 FILE_CHUNK  bytes, at most 256 KiB
    0x62 FILE_END    -
    0x63 FILE_ABORT  str reason

`flags` bit 0 marks a batch that came from the sender's clipboard rather than a
drag. The receiver writes those files to disk either way, they have to go
somewhere, but also puts them on its own clipboard once the last one arrives,
so the user's next paste produces the files rather than nothing.

`name` is chosen by the sender and so is attacker-controlled. The receiver must
reduce it to a single path component, no separators, no `..`, no reserved DOS
device name, no trailing dot or space, and must never overwrite an existing
file. A rejected file is answered with ABORT and does not end the session.

## Session messages

    0x30 ENTER   u16 x, u16 y   sender took control; receiver shows cursor at x,y
    0x31 LEAVE   -              sender released control; receiver releases everything held
    0x40 PING    u64 id         either direction
    0x41 PONG    u64 id         echo of the id

`ENTER` and `LEAVE` also travel both ways. A machine that receives `ENTER` must
stop its own edge detection until `LEAVE`, or both ends will try to own one
pointer and it will sit still on both.

Only the Mac sends `PING`, because only the Mac can reconnect: the agent drops
a session that has gone quiet for 2 s, so an idle Mac still has to prove the
link is alive whichever way control is flowing.

## Dead-man switch

While the Mac holds control it sends `PING` every 500 ms. If the agent sees no
message for 2 s it releases every held key and button. If the Mac sees no
`PONG` for 2 s it releases capture and returns the cursor to the Mac, losing
Wi-Fi must never leave the user with no cursor on either machine.

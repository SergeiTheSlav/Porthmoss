# Third-party notices

The Mac app has no third-party dependencies. It is SwiftUI, CoreGraphics and
Network, all of them Apple's. Everything below belongs to the Windows agent.

The same list appears in the About panel of the Mac app, in the About card of
the agent's window, and in `porthmoss-agent.exe --version`.

| Project | What it does here | Licence |
| --- | --- | --- |
| [energye/systray](https://github.com/energye/systray) | the agent's notification-area icon | Apache 2.0 |
| [jchv/go-webview2](https://github.com/jchv/go-webview2) | the agent's window | MIT |
| [jchv/go-winloader](https://github.com/jchv/go-winloader) | loading WebView2 without a DLL on disk | ISC |
| [libp2p/zeroconf](https://github.com/libp2p/zeroconf) | mDNS, so the Mac can find the PC | MIT |
| [miekg/dns](https://github.com/miekg/dns) | the DNS records mDNS discovery is made of | BSD 3-clause |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | the Win32 calls behind every injected keystroke | BSD 3-clause |

Each project's full licence text ships in its own repository and in the Go
module cache. `go mod download` followed by a look in `$(go env GOMODCACHE)`
produces it verbatim.

Porthmoss itself is MIT licensed. See [LICENSE](LICENSE).

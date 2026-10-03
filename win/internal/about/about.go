// Package about carries who wrote the agent, what it may be used for, and what
// it is built on.
//
// The numbers here mirror mac/Sources/PorthmossCore/About.swift, which is the
// Mac side's single source for the same thing. `make check-version` fails the
// build if the two drift apart — two halves of one product reporting different
// versions is exactly the kind of thing nobody notices until a bug report
// quotes both.
package about

// The release this binary is part of.
const (
	Name       = "Porthmoss"
	Version    = "0.2.0"
	Author     = "Jan Jamscikov"
	Repository = "https://github.com/SergeiTheSlav/Porthmoss"
	Licence    = "MIT"
	Copyright  = "© 2026 Jan Jamscikov"
)

// Summary is one line, for the log header and --version.
const Summary = Name + " " + Version + " — " + Licence + " licence — " + Repository

// Credit is something the agent is built on and owes a mention to.
type Credit struct {
	Name string `json:"name"`
	// Role says what it actually does here. A bare list of module paths is a
	// licence notice, not a credit.
	Role    string `json:"role"`
	Licence string `json:"licence"`
	URL     string `json:"url"`
}

// Acknowledgements is everything the agent links in. The Mac app has no
// third-party dependencies of its own, and shows this same list, because the
// two halves are one product.
var Acknowledgements = []Credit{
	{"energye/systray", "this notification-area icon", "Apache 2.0",
		"https://github.com/energye/systray"},
	{"jchv/go-webview2", "this window", "MIT",
		"https://github.com/jchv/go-webview2"},
	{"jchv/go-winloader", "loading WebView2 without a DLL on disk", "ISC",
		"https://github.com/jchv/go-winloader"},
	{"libp2p/zeroconf", "letting your Mac find this PC", "MIT",
		"https://github.com/libp2p/zeroconf"},
	{"miekg/dns", "the DNS records that discovery is made of", "BSD 3-clause",
		"https://github.com/miekg/dns"},
	{"golang.org/x/sys", "the Win32 calls behind every injected keystroke", "BSD 3-clause",
		"https://pkg.go.dev/golang.org/x/sys"},
}

// IsKnownLink reports whether a URL is one this project actually points at.
//
// The window's links all come from the constants above, so this can only ever
// fail if something else is driving the binding — which is exactly the case
// worth refusing. An agent that opens arbitrary URLs on request is a nicer
// target than one that opens six.
func IsKnownLink(url string) bool {
	if url == Repository {
		return true
	}
	for _, credit := range Acknowledgements {
		if credit.URL == url {
			return true
		}
	}
	return false
}

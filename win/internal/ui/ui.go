// Package ui presents the agent's state to whoever is sitting at the PC.
package ui

import (
	"fmt"

	"github.com/SergeiTheSlav/Porthmoss/win/internal/about"
)

// State is everything the window shows. It is a plain snapshot: the agent
// rebuilds and pushes the whole thing rather than sending deltas, because it
// changes a handful of times per session and correctness beats cleverness.
type State struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// Tone drives the status dot: "", "ok", "busy" or "warn".
	Tone string `json:"tone"`

	Address     string `json:"address"`
	Display     string `json:"display"`
	Peer        string `json:"peer"`
	Fingerprint string `json:"fingerprint"`
	// Code is the pairing code, shown only while an unpaired Mac is waiting.
	Code   string `json:"code"`
	Paired bool   `json:"paired"`
	// Controlled means a Mac is driving this PC right now.
	Controlled bool `json:"controlled"`
	// LastFile is the most recent file dragged over from the Mac.
	LastFile string `json:"lastFile"`
	// DropDir is the folder those files land in.
	DropDir string `json:"dropDir"`

	// About is who made this and what it is built on. Constant for the life of
	// the process, and carried in the snapshot anyway: the page is a function
	// of one state object, and the replay that happens when the window finishes
	// loading then covers the credits with no extra plumbing.
	About AboutInfo `json:"about"`
}

// AboutInfo is the credits, in the shape the page renders.
type AboutInfo struct {
	Name       string         `json:"name"`
	Version    string         `json:"version"`
	Author     string         `json:"author"`
	Repository string         `json:"repository"`
	Licence    string         `json:"licence"`
	Copyright  string         `json:"copyright"`
	Credits    []about.Credit `json:"credits"`
}

// Credits returns the fixed half of every state snapshot.
func Credits() AboutInfo {
	return AboutInfo{
		Name:       about.Name,
		Version:    about.Version,
		Author:     about.Author,
		Repository: about.Repository,
		Licence:    about.Licence,
		Copyright:  about.Copyright,
		Credits:    about.Acknowledgements,
	}
}

// UI is the agent's front end. The console implementation is the fallback and
// the one that works everywhere; the Windows one adds a tray icon and window.
type UI interface {
	// Update replaces the displayed state. Safe to call from any goroutine.
	Update(State)
	// Run blocks until the user quits. It must be called from the main
	// goroutine: the Windows tray owns the main thread.
	Run() error
	// Stop asks Run to return.
	Stop()
}

// Options configures a UI.
type Options struct {
	// StartHidden keeps the window out of the way on launch, for an agent that
	// is already paired and has nothing to tell the user.
	StartHidden bool

	// OnUnpair is invoked when the user asks to forget the paired Mac.
	OnUnpair func()
	// OnQuit is invoked when the user quits from the tray.
	OnQuit func()

	// OnOpenDropFolder is invoked when the user asks to see the files that
	// have arrived from the Mac.
	OnOpenDropFolder func()

	// OnOpenLink is invoked when the user clicks one of the credits links.
	// It must open the URL in the user's browser: following it inside the
	// window would replace the status page with a web page and leave no way
	// back, since the window has no navigation chrome of its own.
	OnOpenLink func(url string)
}

// GroupFingerprint breaks a hex fingerprint into 4-character groups, so it can
// be compared against the Mac's screen without reading 64 undifferentiated
// characters.
func GroupFingerprint(hex string) string {
	out := make([]byte, 0, len(hex)+len(hex)/4)
	for i := 0; i < len(hex); i += 4 {
		if i > 0 {
			out = append(out, ' ')
		}
		end := min(i+4, len(hex))
		out = append(out, hex[i:end]...)
	}
	return string(out)
}

// DescribeMonitors renders a display layout for the "Display" row.
func DescribeMonitors(width, height int32, count int) string {
	if count <= 1 {
		return fmt.Sprintf("%d × %d", width, height)
	}
	return fmt.Sprintf("%d × %d across %d monitors", width, height, count)
}

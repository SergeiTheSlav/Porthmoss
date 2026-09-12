// Package ui presents the agent's state to whoever is sitting at the PC.
package ui

import "fmt"

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
	// OnUnpair is invoked when the user asks to forget the paired Mac.
	OnUnpair func()
	// OnQuit is invoked when the user quits from the tray.
	OnQuit func()
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

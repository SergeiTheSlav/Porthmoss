// Package safe keeps one failing goroutine from taking the whole agent down.
package safe

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Logger is set once at startup so recovered panics reach the same place as
// everything else, a log file, since a -H windowsgui build has no console for
// them to print to.
var Logger *slog.Logger

// Go runs fn in a goroutine that survives a panic instead of crashing the
// process. The agent lives in the tray for hours; a single bad clipboard read
// or network event must not end the session, and must not vanish without a
// trace.
func Go(what string, fn func()) {
	go Do(what, fn)
}

// Do runs fn now, recovering and logging any panic. Use it inside a
// syscall.NewCallback: a panic unwinding back into C is undefined behaviour,
// so it has to be stopped before it gets there.
func Do(what string, fn func()) {
	defer Recover(what)
	fn()
}

// Recover logs a panic and swallows it. Deferred at the top of anything that
// must not be allowed to kill the process.
func Recover(what string) {
	if r := recover(); r != nil {
		msg := fmt.Sprintf("recovered from panic in %s: %v", what, r)
		if Logger != nil {
			Logger.Error(msg, "stack", string(debug.Stack()))
		}
	}
}

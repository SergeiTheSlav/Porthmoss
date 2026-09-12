//go:build !windows

package ui

// New returns the console front end on non-Windows hosts. The agent is a
// Windows program; this keeps the package building on the Mac, where the rest
// of the repo is developed and tested.
func New(opts Options) UI { return NewConsole() }

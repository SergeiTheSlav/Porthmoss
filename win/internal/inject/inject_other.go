//go:build !windows

package inject

// New returns a recording injector on non-Windows hosts. The agent is a Windows
// program; this exists so the server and protocol can be developed and tested
// from the Mac side of the repo.
func New() (*Fake, error) { return NewFake(), nil }

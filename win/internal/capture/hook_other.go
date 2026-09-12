//go:build !windows

package capture

import "log/slog"

// This file keeps the package building on the Mac, where the rest of the repo
// is developed and where `make test-go` runs. Everything that can be tested
// without Windows — the crossing rules and the controller that turns them into
// messages — lives in model.go and controller.go and is exercised there; only
// the two low-level hooks and the pointer parking need a real PC.

// Hook does nothing off Windows. There is no portable way to swallow the
// system's own input, and nothing to swallow it from.
type Hook struct{}

// NewHook returns a hook that captures nothing.
func NewHook(*Controller, *slog.Logger) *Hook { return &Hook{} }

// Start reports success without installing anything, so an agent built for a
// Mac still runs as a target.
func (*Hook) Start() error { return nil }

func (*Hook) Stop() {}

// nopPointer stands in for the Win32 pointer control. The model still runs;
// nothing pins a cursor.
type nopPointer struct{}

// NewPointer returns a pointer control that does nothing.
func NewPointer() Pointer { return nopPointer{} }

// Park reports an anchor at the middle of the display, which is what the
// Windows one picks, so the model behaves the same way.
func (nopPointer) Park(within Rect) (Point, error) {
	return Point{X: within.X + within.W/2, Y: within.Y + within.H/2}, nil
}

func (nopPointer) Warp(Point) {}

func (nopPointer) Release(Point) error { return nil }

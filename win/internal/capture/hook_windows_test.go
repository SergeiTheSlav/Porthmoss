package capture

import (
	"testing"
	"time"
)

// TestHookInstallsAndComesBackDown is the part of the Windows half that can be
// checked with no Mac at the other end: that the two low-level hooks install
// on a thread of their own, and that taking them down again does not wedge.
//
// It captures nothing while it runs. No session is attached, so the controller
// passes every event straight through — which makes this safe to run on a
// machine somebody is sitting at, and is also why it proves only installation
// and teardown, not capture.
//
// It skips itself rather than failing when the hooks will not install: group
// policy and locked-down machines both refuse, and the agent treats that as a
// missing feature rather than an error, so the test should not be stricter
// than the product.
func TestHookInstallsAndComesBackDown(t *testing.T) {
	controller := New(Options{})
	t.Cleanup(controller.Close)

	hook := NewHook(controller, nil)
	if err := hook.Start(); err != nil {
		t.Skipf("this machine will not allow low-level hooks: %v", err)
	}

	// Starting twice is a no-op, because the agent may well try again.
	if err := hook.Start(); err != nil {
		t.Errorf("a second Start should do nothing, got %v", err)
	}

	// Stop waits for the hook thread, so a message loop that never sees the
	// quit message shows up here as a hang rather than as mystery latency in
	// somebody's shutdown.
	stopped := make(chan struct{})
	go func() {
		hook.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return: the hook thread's message loop is wedged")
	}

	// Stopping again must not block on an already-closed thread either.
	hook.Stop()
}

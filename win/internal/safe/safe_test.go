package safe

import (
	"sync"
	"testing"
)

// TestGoSurvivesAPanic is the whole point of the package: a panicking
// goroutine must not take the process down with it.
func TestGoSurvivesAPanic(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	Go("test", func() {
		defer wg.Done()
		panic("boom")
	})
	wg.Wait() // if the panic escaped, the test binary would have crashed
}

// TestDoReturnsAfterRecovering: Do swallows the panic and returns normally.
func TestDoReturnsAfterRecovering(t *testing.T) {
	ran := false
	Do("test", func() {
		ran = true
		panic("boom")
	})
	if !ran {
		t.Fatal("fn did not run")
	}
	// Reaching here at all means Do recovered.
}

func TestDoRunsCleanly(t *testing.T) {
	got := 0
	Do("test", func() { got = 42 })
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

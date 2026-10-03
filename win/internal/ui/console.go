package ui

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/SergeiTheSlav/Porthmoss/win/internal/about"
)

// Console is the no-window front end: what `--console` selects, what runs on
// non-Windows hosts, and the fallback when the WebView2 runtime is missing.
type Console struct {
	mu   sync.Mutex
	last State
	done chan struct{}
	once sync.Once
}

func NewConsole() *Console {
	fmt.Println(about.Summary)
	return &Console{done: make(chan struct{})}
}

func (c *Console) Update(state State) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Only print what changed; the agent pushes full snapshots.
	if state.Code != "" && state.Code != c.last.Code {
		fmt.Printf("\n  Pairing code: %s\n  Enter this in Porthmoss on your Mac.\n\n", state.Code)
	}
	if state.Title != c.last.Title || state.Detail != c.last.Detail {
		if state.Detail != "" {
			fmt.Printf("%s — %s\n", state.Title, state.Detail)
		} else {
			fmt.Println(state.Title)
		}
	}
	c.last = state
}

func (c *Console) Run() error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-signals:
	case <-c.done:
	}
	return nil
}

func (c *Console) Stop() { c.once.Do(func() { close(c.done) }) }

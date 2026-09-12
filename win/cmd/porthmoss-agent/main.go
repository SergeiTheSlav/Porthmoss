// Command porthmoss-agent runs on the Windows PC and applies input sent by a
// paired Mac. It needs no driver and no elevation for ordinary desktop use.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/janjamscikov/porthmoss/win/internal/discovery"
	"github.com/janjamscikov/porthmoss/win/internal/inject"
	"github.com/janjamscikov/porthmoss/win/internal/pairing"
	"github.com/janjamscikov/porthmoss/win/internal/server"
	"github.com/janjamscikov/porthmoss/win/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "porthmoss-agent:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		port    = flag.Int("port", server.DefaultPort, "TCP port to listen on")
		bind    = flag.String("bind", "", "address to bind (default: all interfaces)")
		stateIn = flag.String("state", "", "state directory (default: per-user app data)")
		name    = flag.String("name", "", "name advertised over mDNS (default: hostname)")
		noMDNS  = flag.Bool("no-mdns", false, "do not advertise over mDNS")
		console = flag.Bool("console", false, "run without the tray icon and window")
		unpair  = flag.Bool("unpair", false, "forget the paired Mac and exit")
		verbose = flag.Bool("v", false, "verbose logging")
	)
	flag.Parse()

	// Built as a GUI binary so the tray app does not drag a console window
	// around; --console reattaches to the terminal that launched it.
	if *console || *verbose {
		attachConsole()
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	quietMDNSLogging(*verbose)

	stateDir := *stateIn
	if stateDir == "" {
		var err error
		if stateDir, err = defaultStateDir(); err != nil {
			return err
		}
	}
	identity, err := pairing.Load(stateDir)
	if err != nil {
		return err
	}

	if *unpair {
		if err := identity.Unpair(); err != nil {
			return err
		}
		fmt.Println("Unpaired. The next Mac to connect will be shown a new pairing code.")
		return nil
	}

	injector, err := inject.New()
	if err != nil {
		return fmt.Errorf("input backend: %w", err)
	}
	screens, err := injector.Screens()
	if err != nil {
		return fmt.Errorf("reading the display layout: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	presenter := &presenter{
		identity: identity,
		display:  ui.DescribeMonitors(screens.Virtual.Width, screens.Virtual.Height, len(screens.Monitors)),
	}

	var front ui.UI
	if *console {
		front = ui.NewConsole()
	} else {
		front = ui.New(ui.Options{
			OnUnpair: func() {
				if err := identity.Unpair(); err != nil {
					log.Error("unpair failed", "err", err)
					return
				}
				log.Info("unpaired on request")
				presenter.publish()
			},
			OnQuit: cancel,
		})
	}
	presenter.ui = front

	if !*noMDNS {
		if err := discovery.Advertise(ctx, *name, *port, identity.FingerprintHex()); err != nil {
			// Discovery is a convenience; the Mac can still connect by address.
			log.Warn("mDNS advertisement failed, connect by address instead", "err", err)
		}
	}

	srv := &server.Server{
		Addr:          net.JoinHostPort(*bind, strconv.Itoa(*port)),
		Identity:      identity,
		Injector:      injector,
		Log:           log,
		OnPairingCode: presenter.setCode,
		OnListening:   presenter.setAddress,
		OnSession:     presenter.setSession,
	}

	presenter.publish()

	// The server runs in the background; the UI owns the main thread, because
	// the Windows notification area insists on it.
	errs := make(chan error, 1)
	go func() { errs <- srv.Serve(ctx) }()

	go func() {
		if err := <-errs; err != nil {
			log.Error("server stopped", "err", err)
			front.Stop()
		}
	}()

	return front.Run()
}

// presenter turns agent events into the single snapshot the UI renders.
type presenter struct {
	ui       ui.UI
	identity *pairing.Identity
	display  string

	mu        sync.Mutex
	address   string
	code      string
	peer      string
	connected bool
}

func (p *presenter) setAddress(addr string) {
	p.mu.Lock()
	p.address = addr
	p.mu.Unlock()
	p.publish()
}

func (p *presenter) setCode(code string) {
	p.mu.Lock()
	p.code = code
	p.mu.Unlock()
	p.publish()
}

func (p *presenter) setSession(connected bool, peer string) {
	p.mu.Lock()
	p.connected = connected
	p.peer = peer
	if connected {
		// The code has done its job and must not linger on screen.
		p.code = ""
	}
	p.mu.Unlock()
	p.publish()
}

func (p *presenter) publish() {
	p.mu.Lock()
	state := ui.State{
		Address:     p.address,
		Display:     p.display,
		Code:        p.code,
		Fingerprint: ui.GroupFingerprint(p.identity.FingerprintHex()),
		Paired:      p.identity.Paired(),
	}
	switch {
	case p.connected:
		state.Title = "Controlled by " + p.peer
		state.Detail = "Your Mac is driving this PC."
		state.Tone = "ok"
	case p.code != "":
		state.Title = "Waiting to pair"
		state.Detail = "Enter the code below on your Mac."
		state.Tone = "busy"
	case p.identity.Paired():
		state.Title = "Ready"
		state.Detail = "Waiting for your Mac to connect."
		state.Tone = "ok"
	default:
		state.Title = "Not paired"
		state.Detail = "Connect from your Mac to pair this PC."
		state.Tone = "warn"
	}
	if p.identity.Paired() {
		state.Peer = "Paired"
	}
	p.mu.Unlock()

	if p.ui != nil {
		p.ui.Update(state)
	}
}

func defaultStateDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	return filepath.Join(dir, "Porthmoss"), nil
}

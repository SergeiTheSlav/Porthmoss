// Command porthmoss-agent runs on the Windows PC and applies input sent by a
// paired Mac. It needs no driver and no elevation for ordinary desktop use.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/janjamscikov/porthmoss/win/internal/clipboard"
	"github.com/janjamscikov/porthmoss/win/internal/discovery"
	"github.com/janjamscikov/porthmoss/win/internal/inject"
	"github.com/janjamscikov/porthmoss/win/internal/pairing"
	"github.com/janjamscikov/porthmoss/win/internal/safe"
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

	stateDir := *stateIn
	if stateDir == "" {
		var err error
		if stateDir, err = defaultStateDir(); err != nil {
			return err
		}
	}

	// Log to a file as well as stderr. Built with -H windowsgui, the agent has
	// no console, so anything printed to stderr goes nowhere and the program
	// appears to "just close on its own". The file is where to look when it does.
	logFile := openLogFile(stateDir)
	if logFile != nil {
		defer logFile.Close()
	}
	log := slog.New(slog.NewTextHandler(logWriter(logFile), &slog.HandlerOptions{Level: level}))
	safe.Logger = log
	quietMDNSLogging(*verbose)

	// A panic on the main goroutine still ends the process, but now it says so
	// in the log first rather than vanishing.
	defer safe.Recover("main")
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

	dropDir, err := defaultDropDir()
	if err != nil {
		return err
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
			// Nothing to say yet: sit in the tray rather than in front of
			// whatever the user is doing.
			StartHidden: identity.Paired(),
			OnUnpair: func() {
				if err := identity.Unpair(); err != nil {
					log.Error("unpair failed", "err", err)
					return
				}
				log.Info("unpaired on request")
				presenter.publish()
			},
			OnQuit: cancel,
			OnOpenDropFolder: func() {
				if err := openFolder(dropDir); err != nil {
					log.Warn("could not open the drop folder", "err", err)
				}
			},
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
		Addr:      net.JoinHostPort(*bind, strconv.Itoa(*port)),
		Identity:  identity,
		Injector:  injector,
		Clipboard: clipboard.New(),
		DropDir:   dropDir,
		OnFileReceived: func(path string, _, _ int) {
			presenter.fileReceived(path)
		},
		Log:           log,
		OnPairingCode: presenter.setCode,
		OnListening:   presenter.setAddress,
		OnSession:     presenter.setSession,
	}

	presenter.publish()

	// The server runs in the background; the UI owns the main thread, because
	// the Windows notification area insists on it.
	errs := make(chan error, 1)
	go func() {
		defer safe.Recover("server")
		errs <- srv.Serve(ctx)
	}()

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
	lastFile  string
	dropDir   string
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

// fileReceived notes an arrival so the window can mention it. Kept to the last
// one: a running log belongs in the folder, not in a status panel.
func (p *presenter) fileReceived(path string) {
	p.mu.Lock()
	p.lastFile = filepath.Base(path)
	p.dropDir = filepath.Dir(path)
	p.mu.Unlock()
	p.publish()
}

func (p *presenter) publish() {
	p.mu.Lock()
	state := ui.State{
		LastFile:    p.lastFile,
		DropDir:     p.dropDir,
		Address:     p.address,
		Display:     p.display,
		Code:        p.code,
		Fingerprint: ui.GroupFingerprint(p.identity.FingerprintHex()),
		Paired:      p.identity.Paired(),
	}
	switch {
	case p.connected:
		state.Controlled = true
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

// defaultDropDir is where files dragged from the Mac land. Downloads is where
// a user already looks for something that arrived from elsewhere.
func defaultDropDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, "Downloads", "Porthmoss"), nil
}

// openLogFile opens (truncating) the log next to the agent's state. A failure
// here is not worth aborting for: the agent runs fine without a log, it is just
// harder to debug, so this returns nil and carries on.
func openLogFile(stateDir string) *os.File {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil
	}
	// Truncate on launch: this is a "why did it die last time" record, not an
	// archive, and an unbounded log on a long-running machine is its own bug.
	f, err := os.OpenFile(filepath.Join(stateDir, "porthmoss.log"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil
	}
	return f
}

// logWriter sends output to the file, and also to stderr when there is a
// console to see it.
func logWriter(f *os.File) io.Writer {
	if f == nil {
		return os.Stderr
	}
	if hasConsole() {
		return io.MultiWriter(f, os.Stderr)
	}
	return f
}

func defaultStateDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	return filepath.Join(dir, "Porthmoss"), nil
}

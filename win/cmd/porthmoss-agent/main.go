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
	"time"

	"github.com/janjamscikov/porthmoss/win/internal/capture"
	"github.com/janjamscikov/porthmoss/win/internal/clipboard"
	"github.com/janjamscikov/porthmoss/win/internal/discovery"
	"github.com/janjamscikov/porthmoss/win/internal/inject"
	"github.com/janjamscikov/porthmoss/win/internal/pairing"
	"github.com/janjamscikov/porthmoss/win/internal/proto"
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

		// Reverse control: this PC's own mouse and keyboard driving the Mac.
		edge = flag.String("edge", string(capture.DefaultConfig().Edge),
			"edge of this PC's desktop the Mac sits beyond: left, right, top or bottom")
		sensitivity = flag.Float64("sensitivity", capture.DefaultConfig().Sensitivity,
			"scales this PC's mouse movement into Mac pixels")
		noCapture = flag.Bool("no-capture", false,
			"do not capture this PC's input; only act as a target for the Mac")
		probe = flag.Bool("probe-mac", false,
			"install no hooks; instead sweep the Mac's cursor once a Mac connects, "+
				"to test sending separately from capturing")
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

	// The capture path: this PC's own input driving the Mac. It is deliberately
	// an extra rather than a requirement — an agent that cannot install its
	// hooks is still perfectly good as a target, which is what it was built to
	// be first.
	capturer, stopCapture, err := startCapture(captureSetup{
		enabled:     !*noCapture && !*probe,
		edge:        *edge,
		sensitivity: *sensitivity,
		screens:     injector.Screens,
		log:         log,
		onStatus:    presenter.setCapture,
	})
	if err != nil {
		return err
	}
	defer stopCapture()

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

		OnController: func(send server.Sender) {
			switch {
			case *probe && send != nil:
				go probeMac(log, send)
			case capturer == nil:
			case send == nil:
				capturer.Detach()
			default:
				capturer.Attach(capture.Sender(send))
			}
		},
		// While the Mac is driving this PC, this PC must not try to drive the
		// Mac: two machines fighting over one pointer is a cursor that goes
		// nowhere on either.
		// Reverse control is configured on the Mac: which edge leads where, and
		// whether it is wanted at all. Guessing either of those from a flag
		// here is how you end up pushing an edge that nothing listens to.
		OnClientInfo: func(info proto.ClientInfo) {
			if capturer == nil {
				return
			}
			if edge, err := capture.ParseEdge(info.Edge); err == nil {
				config := capturer.Config()
				config.Edge = edge
				capturer.SetConfig(config)
			} else {
				log.Warn("the Mac named an edge this agent does not know", "edge", info.Edge)
			}
			if d := info.Desktop; d.Width > 0 && d.Height > 0 {
				capturer.SetRemote(capture.Rect{
					X: float64(d.Left), Y: float64(d.Top),
					W: float64(d.Width), H: float64(d.Height),
				})
			}
			capturer.SetEnabled(info.ReverseControl())
			log.Info("reverse control configured by the Mac",
				"enabled", info.ReverseControl(), "mac_beyond_edge", info.Edge)
		},

		OnRemoteInput: func() {
			if capturer != nil {
				capturer.NoteDriven()
			}
		},

		OnRemoteControl: func(active bool) {
			if capturer != nil {
				capturer.Suspend(active)
			}
		},
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

// captureSetup is what starting the capture path needs from run().
type captureSetup struct {
	enabled     bool
	edge        string
	sensitivity float64
	screens     func() (proto.ScreenInfo, error)
	log         *slog.Logger
	onStatus    func(capture.Status)
}

// startCapture builds the controller and installs the low-level hooks,
// returning a controller that may be nil and a stop function that is always
// safe to call.
//
// A hook that will not install is reported and stepped over rather than
// treated as fatal. Group policy, another agent that got there first and a
// locked-down machine all produce it, and none of them is a reason to stop the
// Mac being able to drive this PC.
func startCapture(setup captureSetup) (*capture.Controller, func(), error) {
	if !setup.enabled {
		return nil, func() {}, nil
	}
	edge, err := capture.ParseEdge(setup.edge)
	if err != nil {
		return nil, func() {}, err
	}
	if setup.sensitivity <= 0 {
		return nil, func() {}, fmt.Errorf("sensitivity must be greater than zero, got %v", setup.sensitivity)
	}

	config := capture.DefaultConfig()
	config.Edge = edge
	config.Sensitivity = setup.sensitivity

	controller := capture.New(capture.Options{
		Config:   config,
		Screens:  setup.screens,
		Pointer:  capture.NewPointer(),
		Log:      setup.log,
		OnStatus: setup.onStatus,
	})

	hook := capture.NewHook(controller, setup.log)
	if err := hook.Start(); err != nil {
		setup.log.Warn("this PC cannot capture its own input; it can still be driven from the Mac", "err", err)
		controller.Close()
		return nil, func() {}, nil
	}
	setup.log.Info("push the "+string(edge)+" edge of this PC to take over the Mac",
		"escape", "Ctrl+Alt+Win+P")
	return controller, func() {
		// Hooks first: once they are gone the user's input is their own again
		// whatever else is still shutting down.
		hook.Stop()
		controller.Close()
	}, nil
}

// probeMac sweeps the Mac's cursor and nothing else.
//
// It exists to separate "can this PC send?" from "can this PC capture?". The
// receiving half on the Mac is already known-good — `make test-injector`
// proves it — so if this moves the Mac's cursor, everything from here to the
// Mac's own injector is working and any remaining fault is in the hooks.
func probeMac(log *slog.Logger, send server.Sender) {
	const steps = 100
	log.Info("probe: taking the Mac's cursor for two seconds")

	if err := send(proto.TypeEnter, proto.MouseMove{X: 8192, Y: 32768}.Encode()); err != nil {
		log.Error("probe: the Mac would not take ENTER", "err", err)
		return
	}
	for i := range steps {
		x := uint16(8192 + (57343-8192)*i/(steps-1))
		if err := send(proto.TypeMouseMove, proto.MouseMove{X: x, Y: 32768}.Encode()); err != nil {
			log.Error("probe: sending movement failed", "step", i, "err", err)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := send(proto.TypeLeave, nil); err != nil {
		log.Error("probe: the Mac would not take LEAVE", "err", err)
		return
	}
	log.Info("probe: done — if the Mac's cursor swept across, the link is good")
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
	capturing bool
	lastFile  string
	dropDir   string
}

// setCapture notes this PC taking or handing back control of the Mac.
func (p *presenter) setCapture(status capture.Status) {
	p.mu.Lock()
	p.capturing = status.Remote
	p.mu.Unlock()
	p.publish()
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
	case p.capturing:
		// Deliberately not Controlled: that flag hides the window because
		// SendInput lands in whatever is in front, and nothing is being
		// injected here — this PC's input is going the other way.
		state.Capturing = true
		state.Title = "Controlling your Mac"
		state.Detail = "This PC's mouse and keyboard are driving the Mac."
		state.Tone = "ok"
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

func defaultStateDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	return filepath.Join(dir, "Porthmoss"), nil
}

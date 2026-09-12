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
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/janjamscikov/porthmoss/win/internal/discovery"
	"github.com/janjamscikov/porthmoss/win/internal/inject"
	"github.com/janjamscikov/porthmoss/win/internal/pairing"
	"github.com/janjamscikov/porthmoss/win/internal/server"
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
		unpair  = flag.Bool("unpair", false, "forget the paired Mac and exit")
		verbose = flag.Bool("v", false, "verbose logging")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
		OnPairingCode: printPairingCode,
	}
	if !identity.Paired() {
		fmt.Println("Not paired yet. A code will appear here when your Mac connects.")
	}
	return srv.Serve(ctx)
}

func printPairingCode(code string) {
	fmt.Printf("\n  Pairing code: %s\n  Enter this on your Mac to finish pairing.\n\n", code)
}

func defaultStateDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	return filepath.Join(dir, "Porthmoss"), nil
}

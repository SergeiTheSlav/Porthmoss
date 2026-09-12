// Package discovery advertises the agent over mDNS so the Mac can find it
// without the user typing an IP address.
package discovery

import (
	"context"
	"fmt"
	"os"

	"github.com/libp2p/zeroconf/v2"
)

// ServiceType is the Bonjour service the Mac browses for.
const ServiceType = "_porthmoss._tcp"

// Advertise publishes this agent until ctx is cancelled. The TXT record carries
// the certificate fingerprint so the Mac can show the user which machine it is
// about to trust before any connection is made.
func Advertise(ctx context.Context, instance string, port int, fingerprint string) error {
	if instance == "" {
		host, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("discovery: hostname: %w", err)
		}
		instance = host
	}
	txt := []string{
		"v=1",
		"fp=" + fingerprint,
	}
	server, err := zeroconf.Register(instance, ServiceType, "local.", port, txt, nil)
	if err != nil {
		return fmt.Errorf("discovery: register %s: %w", ServiceType, err)
	}
	go func() {
		<-ctx.Done()
		server.Shutdown()
	}()
	return nil
}

//go:build !windows

package main

import (
	"io"
	"log"
)

func attachConsole() {}

func quietMDNSLogging(verbose bool) {
	if !verbose {
		log.SetOutput(io.Discard)
	}
}

// hasConsole: on a Mac (where this only builds for development) stderr is real.
func hasConsole() bool { return true }

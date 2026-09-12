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

//go:build windows

package main

import (
	"io"
	"log"
	"os"

	"golang.org/x/sys/windows"
)

// attachConsole reattaches stdio to the terminal that launched us.
//
// The agent is linked as a GUI binary so the tray app does not drag a black
// console window behind it. That also means `--console` starts with no stdio
// at all, which would silently swallow every message it is meant to print.
// consoleAttached records whether attachConsole hooked up a real console,
// so log output can be teed to stderr only when there is one to read it.
var consoleAttached bool

func attachConsole() {
	consoleAttached = true
	const attachParentProcess = ^uint32(0) // (DWORD)-1
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	if err := kernel32.NewProc("AttachConsole").Find(); err != nil {
		return
	}
	if ret, _, _ := kernel32.NewProc("AttachConsole").Call(uintptr(attachParentProcess)); ret == 0 {
		// No parent console (launched from Explorer): make our own.
		kernel32.NewProc("AllocConsole").Call()
	}
	for _, redirect := range []struct {
		name string
		file **os.File
	}{{"CONOUT$", &os.Stdout}, {"CONOUT$", &os.Stderr}, {"CONIN$", &os.Stdin}} {
		if f, err := os.OpenFile(redirect.name, os.O_RDWR, 0); err == nil {
			*redirect.file = f
		}
	}
	log.SetOutput(os.Stderr)
}

// quietMDNSLogging silences the mDNS library's direct use of the standard
// logger. It warns once per interface that cannot do multicast, which on a
// Windows box means a line each for Hyper-V, WSL, VPN and Bluetooth adapters —
// alarming to read, and every one of them is expected.
func quietMDNSLogging(verbose bool) {
	if verbose {
		log.SetPrefix("mdns: ")
		return
	}
	log.SetOutput(io.Discard)
}

// hasConsole reports whether stderr goes anywhere a person can see.
func hasConsole() bool { return consoleAttached }

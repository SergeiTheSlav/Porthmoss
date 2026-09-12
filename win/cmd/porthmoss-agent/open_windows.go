//go:build windows

package main

import (
	"os"
	"os/exec"
)

// openFolder shows a directory in Explorer, creating it first so the button
// does nothing surprising before the first file has arrived.
func openFolder(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return exec.Command("explorer.exe", dir).Start()
}

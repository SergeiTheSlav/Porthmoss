//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func openFolder(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return exec.Command("open", dir).Start()
}

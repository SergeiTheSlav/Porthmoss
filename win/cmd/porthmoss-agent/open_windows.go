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

// openInBrowser hands a URL to whatever the user browses with.
//
// url.dll's protocol handler is the one that does not involve a shell: no
// quoting rules to get wrong, and no `cmd /c start`, whose first argument is a
// window title and silently swallows the URL if you forget that.
func openInBrowser(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

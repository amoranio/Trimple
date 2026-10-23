//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

// openWindow is for development on non-Windows machines.
func openWindow(url string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}

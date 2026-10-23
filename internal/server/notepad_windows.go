//go:build windows

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func openInNotepad(path string) error {
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "notepad.exe")
	if _, err := os.Stat(exe); err != nil {
		exe = "notepad.exe"
	}
	cmd := exec.Command(exe, path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

var getLogicalDrives = syscall.NewLazyDLL("kernel32.dll").NewProc("GetLogicalDrives")

// listDrives uses the drive bitmap rather than probing A: to Z:, which can stall for a long time on
// disconnected network drives.
func listDrives() []string {
	mask, _, _ := getLogicalDrives.Call()
	var drives []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) != 0 {
			drives = append(drives, string(rune('A'+i))+`:\`)
		}
	}
	return drives
}

//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	user32        = syscall.NewLazyDLL("user32.dll")
	attachConsole = kernel32.NewProc("AttachConsole")
	messageBoxW   = user32.NewProc("MessageBoxW")
)

// The release build is a GUI-subsystem program (no console window). When started from a terminal,
// attach to it so --help, --version and log lines are visible.
func attachConsoleIfAny() {
	// If stdout is already usable (redirected to a pipe or file by the caller) leave it alone.
	if _, err := os.Stdout.Stat(); err == nil {
		return
	}
	const attachParentProcess = ^uintptr(0) // (DWORD)-1
	if r, _, _ := attachConsole.Call(attachParentProcess); r != 0 {
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stdout, os.Stderr = f, f
		}
	}
}

var setStdHandle = kernel32.NewProc("SetStdHandle")

// ensureStderr points stderr at the log file when the process has none (windowless exe), so that a
// crash leaves a stack trace behind instead of vanishing.
func ensureStderr(f *os.File) {
	if f == nil {
		return
	}
	if _, err := os.Stderr.Stat(); err == nil {
		return
	}
	const stdErrorHandle = ^uintptr(11) // (DWORD)-12
	setStdHandle.Call(stdErrorHandle, f.Fd())
	os.Stderr = f
}

func showError(title, msg string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(msg)
	messageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10) // MB_ICONERROR
}

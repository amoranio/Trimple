//go:build !windows

package server

func openInNotepad(path string) error { return errNotepadUnsupported }

func listDrives() []string { return nil }

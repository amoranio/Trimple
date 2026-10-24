//go:build !windows

package notes

func isSharingViolation(error) bool { return false }

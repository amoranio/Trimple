//go:build windows

package notes

import (
	"errors"
	"syscall"
)

// ERROR_SHARING_VIOLATION (32) and ERROR_LOCK_VIOLATION (33): somebody else has the file open.
func isSharingViolation(err error) bool {
	var en syscall.Errno
	return errors.As(err, &en) && (en == 32 || en == 33)
}

//go:build windows

package notes

import (
	"io/fs"
	"syscall"
)

// hiddenOrSystem is true for folders Windows marks hidden or system (AppData, $Recycle.Bin,
// System Volume Information ...), which must never be walked looking for notes.
func hiddenOrSystem(fi fs.FileInfo) bool {
	if a, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return a.FileAttributes&(syscall.FILE_ATTRIBUTE_HIDDEN|syscall.FILE_ATTRIBUTE_SYSTEM) != 0
	}
	return false
}

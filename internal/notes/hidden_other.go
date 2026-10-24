//go:build !windows

package notes

import "io/fs"

func hiddenOrSystem(fs.FileInfo) bool { return false }

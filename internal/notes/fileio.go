package notes

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// File is a note read from disk.
type File struct {
	Text     string // as on disk, original line endings
	Kind     Kind
	EOL      string
	Lossless bool
	Problem  string
	Hash     string // content hash, used to detect outside changes at save time
	Stamp    string // cheap mtime:size stamp, used for polling
}

// Hash is the content fingerprint used for conflict detection.
func Hash(b []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return fmt.Sprintf("%016x", h.Sum64())
}

// StampOf returns the mtime:size stamp of a file, or "" when it does not exist.
func StampOf(path string) string {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return ""
	}
	return stampFromInfo(fi)
}

func stampFromInfo(fi fs.FileInfo) string {
	return fmt.Sprintf("%d:%d", fi.ModTime().UnixNano(), fi.Size())
}

// retry runs fn up to attempts times, waiting step*n between tries, for errors that are
// plausibly transient (another program briefly holding the file, antivirus, sync clients).
func retry(attempts int, step time.Duration, fn func() error) error {
	var err error
	for i := 1; i <= attempts; i++ {
		err = fn()
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if i < attempts {
			time.Sleep(step * time.Duration(i))
		}
	}
	return err
}

// ReadFile reads and decodes a note.
func ReadFile(path string) (*File, error) {
	var b []byte
	var stamp string
	err := retry(6, 100*time.Millisecond, func() error {
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return fmt.Errorf("%s is a folder", filepath.Base(path))
		}
		stamp = stampFromInfo(fi)
		b, err = os.ReadFile(path)
		return err
	})
	if err != nil {
		return nil, err
	}
	text, kind := Decode(b)
	ok, problem := Check(b, text, kind)
	return &File{
		Text: text, Kind: kind, EOL: DominantEOL(text),
		Lossless: ok, Problem: problem, Hash: Hash(b), Stamp: stamp,
	}, nil
}

// WriteFileAtomic replaces path with data without ever leaving a half-written file: it writes a
// sibling temp file, flushes it to disk, then renames it over the original. A symlinked note is
// written through the link rather than replaced by a regular file.
func WriteFileAtomic(path string, data []byte) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	perm := fs.FileMode(0o666)
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	tmp := path + ".trimple-tmp"
	err := retry(6, 120*time.Millisecond, func() error {
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
		if err != nil {
			return err
		}
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		return os.Rename(tmp, path)
	})
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

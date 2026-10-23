package notes

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// transient reports whether an error is plausibly temporary: another program briefly holding the
// file (editor, antivirus, sync client). Anything else fails immediately.
func transient(err error) bool {
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return errors.Is(err, fs.ErrPermission) || isSharingViolation(err)
}

// retry runs fn up to attempts times, waiting step*n between tries, but only for transient errors.
func retry(attempts int, step time.Duration, fn func() error) error {
	var err error
	for i := 1; i <= attempts; i++ {
		err = fn()
		if err == nil || !transient(err) {
			return err
		}
		if i < attempts {
			time.Sleep(step * time.Duration(i))
		}
	}
	return err
}

// ReadFile reads and decodes a note, waiting out a brief lock.
func ReadFile(path string) (*File, error) { return ReadFileTries(path, 6) }

// ReadFileTries is ReadFile with a chosen number of attempts (the scanner uses few).
func ReadFileTries(path string, attempts int) (*File, error) {
	var b []byte
	var stamp string
	err := retry(attempts, 100*time.Millisecond, func() error {
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

// pathLocks serialises read-modify-write cycles on one file within this process, so two requests
// (a tick and a save, or two ticks) can never overwrite each other's change.
var pathLocks = struct {
	sync.Mutex
	m map[string]*sync.Mutex
}{m: map[string]*sync.Mutex{}}

// LockPath takes the lock for a file; call the returned function to release it. Never nest.
func LockPath(path string) (unlock func()) {
	key := strings.ToLower(filepath.Clean(path))
	pathLocks.Lock()
	l, ok := pathLocks.m[key]
	if !ok {
		l = &sync.Mutex{}
		pathLocks.m[key] = l
	}
	pathLocks.Unlock()
	l.Lock()
	return l.Unlock
}

// WriteFileAtomic replaces path with data without ever leaving a half-written file: it writes a
// uniquely named hidden sibling, flushes it to disk, then renames it over the original. A symlinked
// note is written through the link rather than replaced by a regular file.
func WriteFileAtomic(path string, data []byte) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	perm := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	dir := filepath.Dir(path)
	return retry(6, 120*time.Millisecond, func() error {
		f, err := os.CreateTemp(dir, ".trimple-*.tmp")
		if err != nil {
			return err
		}
		tmp := f.Name()
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Chmod(tmp, perm)
		}
		if err == nil {
			err = os.Rename(tmp, path)
		}
		if err != nil {
			_ = os.Remove(tmp)
		}
		return err
	})
}

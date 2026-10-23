package notes

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	MaxFiles     = 2000
	MaxFileBytes = 5 << 20
)

// FileInfo is one note in the folder, as shown in the file list.
type FileInfo struct {
	Path      string `json:"path"` // relative, forward slashes
	OpenTasks int    `json:"openTasks"`
	Problem   string `json:"problem,omitempty"`
	Stamp     string `json:"stamp"`
}

type entry struct {
	info  FileInfo
	tasks []Task
	err   string
}

// Index keeps the parsed tasks of every .txt file under a root, re-parsing only changed files.
type Index struct {
	mu        sync.RWMutex
	root      string
	entries   map[string]*entry
	version   uint64
	truncated bool
}

func NewIndex(root string) *Index {
	return &Index{root: root, entries: map[string]*entry{}}
}

// SetRoot switches folder and forgets everything.
func (ix *Index) SetRoot(root string) {
	ix.mu.Lock()
	ix.root = root
	ix.entries = map[string]*entry{}
	ix.version++
	ix.truncated = false
	ix.mu.Unlock()
}

func (ix *Index) Root() string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.root
}

type found struct {
	rel, abs, stamp string
}

// listFiles walks root for .txt notes: it skips hidden folders, symlinked/junction folders (loops),
// oversized files and anything past MaxFiles.
func listFiles(root string) (files []found, truncated bool) {
	if root == "" {
		return nil, false
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || d.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".txt") {
			return nil
		}
		fi, err := d.Info()
		if err != nil || fi.Size() > MaxFileBytes {
			return nil
		}
		if len(files) >= MaxFiles {
			truncated = true
			return fs.SkipAll
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		files = append(files, found{rel: filepath.ToSlash(rel), abs: p, stamp: stampFromInfo(fi)})
		return nil
	})
	return files, truncated
}

// Scan brings the index up to date and reports whether anything changed.
func (ix *Index) Scan() (changed bool) {
	root := ix.Root()
	files, truncated := listFiles(root)

	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.root != root { // folder switched while scanning
		return false
	}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f.rel] = true
		if e, ok := ix.entries[f.rel]; ok && e.info.Stamp == f.stamp {
			continue
		}
		changed = true
		e := &entry{info: FileInfo{Path: f.rel, Stamp: f.stamp}}
		file, err := ReadFile(f.abs)
		if err != nil {
			e.err = err.Error()
			e.info.Problem = "cannot be read"
		} else {
			e.tasks = ParseTasks(file.Text, f.rel)
			e.info.Problem = file.Problem
			for _, t := range e.tasks {
				if !t.Done {
					e.info.OpenTasks++
				}
			}
		}
		ix.entries[f.rel] = e
	}
	for rel := range ix.entries {
		if !seen[rel] {
			delete(ix.entries, rel)
			changed = true
		}
	}
	if truncated != ix.truncated {
		ix.truncated = truncated
		changed = true
	}
	if changed {
		ix.version++
	}
	return changed
}

// Forget drops one file so the next Scan re-parses it even if its stamp looks unchanged
// (coarse file-system timestamps could otherwise hide a same-size edit).
func (ix *Index) Forget(rel string) {
	ix.mu.Lock()
	delete(ix.entries, rel)
	ix.mu.Unlock()
}

// Snapshot is a consistent copy of the index.
type Snapshot struct {
	Version   uint64
	Root      string
	Truncated bool
	Tasks     []Task
	Files     []FileInfo
	Errors    []string
}

func (ix *Index) Snapshot() Snapshot {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	s := Snapshot{Version: ix.version, Root: ix.root, Truncated: ix.truncated, Tasks: []Task{}, Files: []FileInfo{}, Errors: []string{}}
	for _, e := range ix.entries {
		s.Tasks = append(s.Tasks, e.tasks...)
		s.Files = append(s.Files, e.info)
		if e.err != "" {
			s.Errors = append(s.Errors, e.info.Path+": "+e.err)
		}
	}
	sort.Slice(s.Files, func(i, j int) bool { return lessPath(s.Files[i].Path, s.Files[j].Path) })
	sort.Strings(s.Errors)
	return s
}

func lessPath(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

// Exists reports whether root is an existing directory.
func Exists(root string) bool {
	fi, err := os.Stat(root)
	return err == nil && fi.IsDir()
}

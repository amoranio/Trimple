package notes

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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
	info    FileInfo
	tasks   []Task
	err     string
	retryAt time.Time // for unreadable files: when to try again
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

// maxVisited bounds how many directory entries one scan may look at, so choosing a huge folder
// (a whole drive) cannot make every scan walk the machine.
const maxVisited = 50000

// listFiles walks root for .txt notes. It skips hidden/system folders, symlinked folders, oversized
// files and anything past MaxFiles. Cloud-sync placeholder files (OneDrive, Dropbox) report an
// unusual file type on Windows, so files are only excluded when they are clearly not files.
func listFiles(root string) (files []found, truncated bool) {
	if root == "" {
		return nil, false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	seenDirs := map[string]bool{realRoot: true} // the root itself counts as visited, so a loop back into it is skipped
	visited := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if visited++; visited > maxVisited {
			truncated = true
			return fs.SkipAll
		}
		name := d.Name()
		if d.IsDir() {
			if p == root {
				return nil
			}
			if strings.HasPrefix(name, ".") || d.Type()&fs.ModeSymlink != 0 {
				return fs.SkipDir
			}
			if fi, err := d.Info(); err != nil || hiddenOrSystem(fi) {
				return fs.SkipDir
			}
			if d.Type()&fs.ModeIrregular != 0 { // mount point / reparse folder: descend once, never in a loop
				real, err := filepath.EvalSymlinks(p)
				if err != nil || !within(realRoot, real) || seenDirs[real] {
					return fs.SkipDir
				}
				seenDirs[real] = true
			}
			return nil
		}
		if d.Type()&(fs.ModeSymlink|fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0 {
			return nil
		}
		if strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".txt") {
			return nil
		}
		fi, err := d.Info()
		if err != nil || fi.IsDir() || fi.Size() > MaxFileBytes {
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

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func parseEntry(f found) *entry {
	e := &entry{info: FileInfo{Path: f.rel, Stamp: f.stamp}}
	file, err := ReadFileTries(f.abs, 2) // the scanner never waits long; it tries again next time
	if err != nil {
		e.err = err.Error()
		e.info.Problem = "cannot be read"
		e.retryAt = time.Now().Add(5 * time.Second)
		return e
	}
	e.tasks = ParseTasks(file.Text, f.rel)
	e.info.Problem = file.Problem
	for _, t := range e.tasks {
		if !t.Done {
			e.info.OpenTasks++
		}
	}
	return e
}

// Scan brings the index up to date and reports whether anything changed. Files are read without
// holding the index lock, so a slow or locked file never stalls requests.
func (ix *Index) Scan() (changed bool) {
	root := ix.Root()
	files, truncated := listFiles(root)

	ix.mu.RLock()
	var todo []found
	now := time.Now()
	for _, f := range files {
		e, ok := ix.entries[f.rel]
		if !ok || e.info.Stamp != f.stamp || (e.err != "" && now.After(e.retryAt)) {
			todo = append(todo, f)
		}
	}
	ix.mu.RUnlock()

	parsed := make(map[string]*entry, len(todo))
	for _, f := range todo {
		parsed[f.rel] = parseEntry(f)
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.root != root { // folder switched while scanning
		return false
	}
	for rel, e := range parsed {
		old, had := ix.entries[rel]
		if !had || old.info.Stamp != e.info.Stamp || (old.err == "") != (e.err == "") || old.info.Problem != e.info.Problem {
			changed = true
		}
		ix.entries[rel] = e
	}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f.rel] = true
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
	for k := range ix.entries {
		if strings.EqualFold(k, rel) {
			delete(ix.entries, k)
		}
	}
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

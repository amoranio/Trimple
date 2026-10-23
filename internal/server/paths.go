package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var errBadPath = errors.New("invalid path")

var reserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}

func isReservedName(name string) bool {
	base := strings.ToUpper(name)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(base, " ")
	if reserved[base] {
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}

// validRel checks a notes-folder-relative path coming from the client. It must be relative,
// use only plain names, and avoid everything Windows treats specially.
func validRel(rel string) error {
	if rel == "" || len(rel) > 500 {
		return fmt.Errorf("%w: empty or too long", errBadPath)
	}
	if strings.ContainsAny(rel, "\x00") || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return fmt.Errorf("%w: must be relative", errBadPath)
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		switch part {
		case ".", "..":
			return fmt.Errorf("%w: %q not allowed", errBadPath, part)
		}
		if strings.ContainsAny(part, `<>:"|?*`) {
			return fmt.Errorf("%w: %q has characters Windows does not allow", errBadPath, part)
		}
		for _, r := range part {
			if r < 0x20 {
				return fmt.Errorf("%w: control character", errBadPath)
			}
		}
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return fmt.Errorf("%w: %q ends with a dot or space", errBadPath, part)
		}
		if isReservedName(part) {
			return fmt.Errorf("%w: %q is a reserved name", errBadPath, part)
		}
	}
	return nil
}

// safeJoin resolves a client-supplied relative path under root and guarantees the result cannot
// escape it, including through symlinks or junctions in any existing part of the path.
func safeJoin(root, rel string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("%w: no notes folder chosen", errBadPath)
	}
	if err := validRel(rel); err != nil {
		return "", err
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	// Resolve the deepest ancestor that exists, then re-attach the remainder.
	probe := abs
	var tail []string
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("%w: cannot resolve", errBadPath)
		}
		tail = append([]string{filepath.Base(probe)}, tail...)
		probe = parent
	}
	realProbe, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return "", err
	}
	if !within(realRoot, realProbe) {
		return "", fmt.Errorf("%w: outside the notes folder", errBadPath)
	}
	return filepath.Join(append([]string{realProbe}, tail...)...), nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// relOf converts an absolute path under root back to a slash-separated relative path.
func relOf(root, abs string) (string, bool) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	realAbs := abs
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		realAbs = r
	}
	if !within(realRoot, realAbs) {
		return "", false
	}
	rel, err := filepath.Rel(realRoot, realAbs)
	if err != nil {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

//go:build windows

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A junction inside the notes folder that points elsewhere must not be a way out of it.
func TestJunctionCannotEscapeNotesFolder(t *testing.T) {
	e := btNew(t, map[string]string{"ok.txt": "- [ ] ok\n"})
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("- [ ] secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(e.root, "toOutside"), outside).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v %s", err, out)
	}
	for _, c := range []struct{ method, url string }{
		{"GET", "/api/file?path=toOutside/secret.txt"},
		{"GET", "/api/stat?path=toOutside/secret.txt"},
	} {
		code, out := e.do(c.method, c.url, nil)
		if c.method == "GET" && c.url[:9] == "/api/stat" {
			if m, ok := out["toOutside/secret.txt"].(map[string]any); !ok || m["exists"] != false {
				t.Errorf("stat through a junction: %d %v", code, out)
			}
			continue
		}
		if code != 400 {
			t.Errorf("%s %s through a junction = %d, want 400", c.method, c.url, code)
		}
	}
	if code, _ := e.do("POST", "/api/task", map[string]any{"path": "toOutside/secret.txt", "line": 1, "done": true, "raw": "- [ ] secret"}); code != 400 {
		t.Errorf("tick through a junction = %d, want 400", code)
	}
	code, _ := e.do("PUT", "/api/file", map[string]any{"path": "toOutside/new.txt", "text": "x", "create": true})
	if code != 400 {
		t.Errorf("create through a junction = %d, want 400", code)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); err == nil {
		t.Error("a note was written outside the notes folder through a junction")
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret.txt")); string(b) != "- [ ] secret\n" {
		t.Errorf("the file outside was modified: %q", b)
	}
}

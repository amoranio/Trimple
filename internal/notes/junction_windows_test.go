//go:build windows

package notes

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v %s", err, out)
	}
}

// NTFS junctions are the Windows way to point a folder somewhere else. The scanner must neither
// follow one out of the notes folder nor loop forever through one that points back in.
func TestJunctionsAreNotFollowedOutOrLooped(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	write(t, outside, "secret.txt", []byte("- [ ] secret\n"))
	write(t, dir, "ok.txt", []byte("- [ ] ok\n"))
	mkJunction(t, filepath.Join(dir, "toOutside"), outside)
	mkJunction(t, filepath.Join(dir, "loop"), dir)

	ix := NewIndex(dir)
	done := make(chan struct{})
	go func() { ix.Scan(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("scan did not finish: a junction loop hangs it")
	}
	var names []string
	for _, f := range ix.Snapshot().Files {
		names = append(names, f.Path)
		if strings.Contains(strings.ToLower(f.Path), "secret") {
			t.Errorf("a junction led the scanner outside the notes folder: %s", f.Path)
		}
	}
	if len(names) != 1 || names[0] != "ok.txt" {
		t.Errorf("indexed %v, want only ok.txt (no duplicates through the loop)", names)
	}
}

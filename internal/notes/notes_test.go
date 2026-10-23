package notes

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func diffBytes(a, b []byte) int {
	if len(a) != len(b) {
		return -1
	}
	n := 0
	for i := range a {
		if a[i] != b[i] {
			n++
		}
	}
	return n
}

func TestParseTaskLine(t *testing.T) {
	task, ok := ParseTaskLine("- [ ] Write report !1 #work #Q3")
	if !ok || task.Done || task.Priority != 1 || task.Text != "Write report" || !reflect.DeepEqual(task.Labels, []string{"work", "q3"}) {
		t.Fatalf("got %+v ok=%v", task, ok)
	}
	for _, l := range []string{"- [x] a", "- [X] a", "* [ ] a", "+ [ ] a", "\t  - [ ] nested"} {
		if _, ok := ParseTaskLine(l); !ok {
			t.Errorf("should be a task: %q", l)
		}
	}
	for _, l := range []string{"", "just text", "# heading #tag", "- not a task", "[ ] no bullet", "-[ ] no space", "- [] empty", "- [ ]", "- [ ]   ", "- [y] x"} {
		if _, ok := ParseTaskLine(l); ok {
			t.Errorf("should not be a task: %q", l)
		}
	}
}

func TestPriorityAndLabelTokens(t *testing.T) {
	cases := []struct {
		line string
		prio int
		lbls []string
		text string
	}{
		{"- [ ] wow!1 ok", 0, []string{}, "wow!1 ok"},
		{"- [ ] go !4 now", 0, []string{}, "go !4 now"},
		{"- [ ] go !3", 3, []string{}, "go"},
		{"- [ ] !2 first", 2, []string{}, "first"},
		{"- [ ] !1 !2 both", 1, []string{}, "both"},
		{"- [ ] read https://x.com/a#frag #real", 0, []string{"real"}, "read https://x.com/a#frag"},
		{"- [ ] fix bug #123", 0, []string{}, "fix bug #123"},
		{"- [ ] a #x #X #x", 0, []string{"x"}, "a"},
		{"- [ ] a #q3-plan, #café", 0, []string{"q3-plan", "café"}, "a ,"},
		{"- [ ] a #", 0, []string{}, "a #"},
		{"- [ ] a ## b", 0, []string{}, "a ## b"},
		{"- [ ] a #a#b", 0, []string{}, "a #a#b"},
		{"- [ ] a #\U0001F600", 0, []string{}, "a #\U0001F600"},
		{"- [ ] a #work:", 0, []string{}, "a #work:"},
		{"- [ ] !1 #work", 1, []string{"work"}, "!1 #work"}, // only tokens: keep something to show
	}
	for _, c := range cases {
		task, ok := ParseTaskLine(c.line)
		if !ok {
			t.Errorf("%q: not parsed", c.line)
			continue
		}
		if task.Priority != c.prio || !reflect.DeepEqual(task.Labels, c.lbls) || task.Text != c.text {
			t.Errorf("%q: got prio=%d labels=%v text=%q; want %d %v %q", c.line, task.Priority, task.Labels, task.Text, c.prio, c.lbls, c.text)
		}
	}
}

func TestParseTasksFencesAndLineNumbers(t *testing.T) {
	text := "a\r\n- [ ] one\n```\r\n- [ ] hidden\r\n```\r- [ ] two\n~~~~\n- [ ] hidden2\n```\n~~~~\n- [ ] three"
	tasks := ParseTasks(text, "f.txt")
	var got []int
	for _, task := range tasks {
		got = append(got, task.Line)
	}
	if !reflect.DeepEqual(got, []int{2, 6, 11}) {
		t.Fatalf("lines = %v (tasks %+v)", got, tasks)
	}
	if len(ParseTasks("", "x")) != 0 {
		t.Fatal("empty text should have no tasks")
	}
	// An unclosed fence hides everything after it.
	if n := len(ParseTasks("```\n- [ ] a\n- [ ] b", "x")); n != 0 {
		t.Fatalf("unclosed fence: %d tasks", n)
	}
	// A shorter closing fence does not close a longer one.
	if n := len(ParseTasks("````\n```\n- [ ] a\n````\n- [ ] b", "x")); n != 1 {
		t.Fatalf("fence length: %d tasks", n)
	}
}

func TestEncodingRoundTripAndLossless(t *testing.T) {
	cases := map[string]struct {
		in   []byte
		kind Kind
		ok   bool
	}{
		"utf8":     {[]byte("héllo\r\n- [ ] a"), UTF8, true},
		"utf8 bom": {append([]byte{0xEF, 0xBB, 0xBF}, "x\n"...), UTF8BOM, true},
		"utf16le":  {append([]byte{0xFF, 0xFE}, encodeUTF16("- [ ] a\r\n", false, nil)...), UTF16LE, true},
		"utf16be":  {append([]byte{0xFE, 0xFF}, encodeUTF16("- [ ] a\r\n", true, nil)...), UTF16BE, true},
		"ansi":     {[]byte{'c', 'a', 'f', 0xE9, 0x0D, 0x0A}, ANSI, true},
		"ansi all high": {func() []byte {
			b := make([]byte, 0, 128)
			for i := 0x80; i <= 0xFF; i++ {
				b = append(b, byte(i))
			}
			return b
		}(), ANSI, true},
		"empty":          {[]byte{}, UTF8, true},
		"utf16 odd":      {[]byte{0xFF, 0xFE, 0x61, 0x00, 0x62}, UTF16LE, false},
		"lone surrogate": {[]byte{0xFF, 0xFE, 0x61, 0x00, 0x00, 0xD8, 0x62, 0x00}, UTF16LE, false},
		"bom+bad byte":   {[]byte{0xEF, 0xBB, 0xBF, 'a', 0xFF, '\n'}, UTF8BOM, false},
		"nul tail":       {[]byte("- [ ] a\n\x00\x00\x00"), UTF8, false},
	}
	for name, c := range cases {
		text, kind := Decode(c.in)
		if kind != c.kind {
			t.Errorf("%s: kind %s, want %s", name, kind, c.kind)
		}
		ok, problem := Check(c.in, text, kind)
		if ok != c.ok {
			t.Errorf("%s: lossless=%v (%s), want %v", name, ok, problem, c.ok)
		}
		if c.ok {
			enc, k := Encode(text, kind)
			if k != kind || !bytes.Equal(enc, c.in) {
				t.Errorf("%s: round trip changed bytes", name)
			}
		} else if problem == "" {
			t.Errorf("%s: a reason must be given", name)
		}
	}
}

func TestAnsiUpgradeWhenUnrepresentable(t *testing.T) {
	enc, k := Encode("café ✓", ANSI)
	if k != UTF8 {
		t.Fatalf("kind = %s, want utf8", k)
	}
	if got, _ := Decode(enc); got != "café ✓" {
		t.Fatalf("decoded %q", got)
	}
}

func TestDominantEOL(t *testing.T) {
	cases := map[string]string{"a\r\nb\r\nc\n": "\r\n", "a\nb\nc\r\n": "\n", "no newline": "\r\n", "a\rb\rc": "\r", "": "\r\n", "a\nb\r": "\n", "a\r\r\n": "\r\n"}
	for in, want := range cases {
		if got := DominantEOL(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestSetTaskDoneChangesExactlyOneByte(t *testing.T) {
	dir := t.TempDir()
	body := "title\r\n- [ ] one !1 #x\r\n- [ ] two\r\ntail without newline"
	files := map[string][]byte{
		"crlf.txt":  []byte(body),
		"lf.txt":    []byte(strings.ReplaceAll(body, "\r\n", "\n")),
		"cr.txt":    []byte(strings.ReplaceAll(body, "\r\n", "\r")),
		"mixed.txt": []byte("a\r\n- [ ] one\n- [ ] two\r- [ ] three\r\n"),
		"bom.txt":   append([]byte{0xEF, 0xBB, 0xBF}, body...),
		"u16le.txt": append([]byte{0xFF, 0xFE}, encodeUTF16(body, false, nil)...),
		"u16be.txt": append([]byte{0xFE, 0xFF}, encodeUTF16(body, true, nil)...),
		"ansi.txt":  append([]byte("- [ ] caf"), 0xE9, '\n'),
	}
	for name, orig := range files {
		p := write(t, dir, name, orig)
		line := 2
		if name == "ansi.txt" {
			line = 1
		}
		if err := SetTaskDone(p, line, true, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, _ := os.ReadFile(p)
		if diffBytes(got, orig) != 1 {
			t.Errorf("%s: %d bytes differ, want exactly 1", name, diffBytes(got, orig))
		}
		if err := SetTaskDone(p, line, false, nil); err != nil {
			t.Fatalf("%s untick: %v", name, err)
		}
		got, _ = os.ReadFile(p)
		if !bytes.Equal(got, orig) {
			t.Errorf("%s: unticking did not restore the original bytes", name)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != len(files) {
		t.Errorf("temp files left behind: %d entries for %d files", len(entries), len(files))
	}
}

func TestSetTaskDoneRefusals(t *testing.T) {
	dir := t.TempDir()
	orig := []byte("- [ ] one\r\n")
	p := write(t, dir, "a.txt", orig)
	wrong := "- [ ] something else"
	if err := SetTaskDone(p, 1, true, &wrong); err == nil {
		t.Error("stale line must be refused")
	}
	for _, line := range []int{0, -3, 9} {
		if err := SetTaskDone(p, line, true, nil); err == nil {
			t.Errorf("line %d must be refused", line)
		}
	}
	notTask := write(t, dir, "b.txt", []byte("plain\n"))
	if err := SetTaskDone(notTask, 1, true, nil); err == nil {
		t.Error("non-task line must be refused")
	}
	bad := []byte{0xEF, 0xBB, 0xBF, '-', ' ', '[', ' ', ']', ' ', 'a', 0xFF, '\n'}
	bp := write(t, dir, "bad.txt", bad)
	if err := SetTaskDone(bp, 1, true, nil); err == nil {
		t.Error("a file that cannot round-trip must be refused")
	}
	if got, _ := os.ReadFile(bp); !bytes.Equal(got, bad) {
		t.Error("refused file was modified")
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, orig) {
		t.Error("refused edits modified the file")
	}
	if err := SetTaskDone(filepath.Join(dir, "missing.txt"), 1, true, nil); err == nil {
		t.Error("missing file must be an error")
	}
}

func TestPathsWithAwkwardNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"my [notes] 1.txt", "a b  c.txt", "it's $home.txt", "café \U0001F600.txt"} {
		orig := []byte("- [ ] one\r\n- [ ] two\r\n")
		p := write(t, dir, "[proj] (1)/"+name, orig)
		if err := SetTaskDone(p, 2, true, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, _ := os.ReadFile(p)
		if diffBytes(got, orig) != 1 {
			t.Errorf("%s: wrong diff", name)
		}
	}
}

func TestWriteFileAtomicThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	real := write(t, dir, "real.txt", []byte("old"))
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks not available:", err)
	}
	if err := WriteFileAtomic(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced by a regular file")
	}
	if got, _ := os.ReadFile(real); string(got) != "new" {
		t.Errorf("target = %q", got)
	}
}

func TestIndexSampleFolder(t *testing.T) {
	ix := NewIndex(filepath.Join("..", "..", "testdata", "sample-notes"))
	if !ix.Scan() {
		t.Fatal("first scan should report changes")
	}
	snap := ix.Snapshot()
	if len(snap.Tasks) != 9 || len(snap.Files) != 3 {
		t.Fatalf("tasks=%d files=%d", len(snap.Tasks), len(snap.Files))
	}
	c := CountOpen(snap.Tasks)
	if c.Total != 7 || c.High != 2 || c.Medium != 3 || c.Low != 1 || c.None != 1 {
		t.Fatalf("counts %+v", c)
	}
	if ix.Scan() {
		t.Error("second scan with no changes must report none")
	}
	// the code-fenced task in Project.txt is ignored
	n := 0
	for _, task := range snap.Tasks {
		if task.Path == "work/Project.txt" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("Project.txt tasks = %d, want 2", n)
	}
}

func TestIndexChangesAndFiltering(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ok.txt", []byte("- [ ] ok\n"))
	write(t, dir, "UPPER.TXT", []byte("- [ ] upper\n"))
	write(t, dir, "no.md", []byte("- [ ] md\n"))
	write(t, dir, "no.txtx", []byte("- [ ] txtx\n"))
	write(t, dir, ".hidden.txt", []byte("- [ ] hidden\n"))
	write(t, dir, ".git/inside.txt", []byte("- [ ] git\n"))
	write(t, dir, "big.txt", make([]byte, MaxFileBytes+1))
	ix := NewIndex(dir)
	ix.Scan()
	var names []string
	for _, f := range ix.Snapshot().Files {
		names = append(names, f.Path)
	}
	if !reflect.DeepEqual(names, []string{"ok.txt", "UPPER.TXT"}) && !reflect.DeepEqual(names, []string{"UPPER.TXT", "ok.txt"}) {
		t.Fatalf("indexed %v", names)
	}
	write(t, dir, "ok.txt", []byte("- [ ] ok\n- [ ] more\n"))
	if !ix.Scan() || len(ix.Snapshot().Tasks) != 3 {
		t.Error("an edited file must be re-parsed")
	}
	os.Remove(filepath.Join(dir, "ok.txt"))
	if !ix.Scan() || len(ix.Snapshot().Tasks) != 1 {
		t.Error("a deleted file must disappear")
	}
	ix.SetRoot(filepath.Join(dir, "missing"))
	ix.Scan()
	if len(ix.Snapshot().Files) != 0 {
		t.Error("a missing folder has no files")
	}
}

func TestIndexCapAndSymlinkLoop(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxFiles+5; i++ {
		write(t, dir, filepath.Join("d", strings.Repeat("0", 1)+string(rune('a'+i%26)), "f"+itoa(i)+".txt"), []byte("- [ ] x\n"))
	}
	if err := os.Symlink(dir, filepath.Join(dir, "loop")); err != nil {
		t.Log("symlink not available:", err)
	}
	ix := NewIndex(dir)
	ix.Scan()
	s := ix.Snapshot()
	if len(s.Files) != MaxFiles || !s.Truncated {
		t.Fatalf("files=%d truncated=%v", len(s.Files), s.Truncated)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func TestSelectAndGroup(t *testing.T) {
	ix := NewIndex(filepath.Join("..", "..", "testdata", "sample-notes"))
	ix.Scan()
	all := ix.Snapshot().Tasks
	if n := len(Select(all, Query{})); n != 7 {
		t.Errorf("default select = %d, want 7", n)
	}
	if n := len(Select(all, Query{ShowDone: true})); n != 9 {
		t.Errorf("show done = %d, want 9", n)
	}
	if n := len(Select(all, Query{Label: "DEV"})); n != 3 {
		t.Errorf("label DEV = %d, want 3", n)
	}
	if n := len(Select(all, Query{Search: "REPORT"})); n != 1 {
		t.Errorf("search = %d, want 1", n)
	}
	if n := len(Select(all, Query{Search: "project.txt"})); n != 2 {
		t.Errorf("search by file = %d, want 2", n)
	}
	open := Select(all, Query{})
	var names []string
	for _, g := range GroupTasks(open, "priority") {
		names = append(names, g.Name)
	}
	if !reflect.DeepEqual(names, []string{"High (!1)", "Medium (!2)", "Low (!3)", "No priority"}) {
		t.Errorf("priority groups %v", names)
	}
	lg := GroupTasks(open, "label")
	if lg[0].Name != "#admin" || lg[len(lg)-1].Name == "" {
		t.Errorf("label groups %+v", lg)
	}
	if g := GroupTasks(all, "priority"); g[0].Tasks[len(g[0].Tasks)-1].Done != true {
		t.Error("done tasks must sort after open ones")
	}
	if len(GroupTasks(nil, "label")) != 0 || len(Select(nil, Query{})) != 0 {
		t.Error("empty input must be safe")
	}
	// a label that looks like a map/JS member name must not break anything
	odd := []Task{{Path: "a.txt", Line: 1, Labels: []string{"keys", "__proto__", "constructor"}, Text: "x"}}
	if got := Labels(odd); len(got) != 3 {
		t.Errorf("labels %v", got)
	}
}

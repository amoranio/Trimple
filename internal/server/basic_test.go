package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const btToken = "test-token"

type btEnv struct {
	t    *testing.T
	root string
	srv  *Server
	ts   *httptest.Server
}

func btNew(t *testing.T, files map[string]string) *btEnv {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(Options{Root: root, ConfigPath: filepath.Join(t.TempDir(), "settings.json"), Token: btToken, KeepAlive: true, ScanEvery: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &btEnv{t: t, root: root, srv: s, ts: ts}
}

func (e *btEnv) do(method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rdr)
	req.Header.Set("X-Trimple-Token", btToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (e *btEnv) read(name string) []byte {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(name)))
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func TestBasicStateAndTick(t *testing.T) {
	e := btNew(t, map[string]string{"a.txt": "- [ ] one !1 #x\r\n- [x] two\r\n", "sub/b.txt": "- [ ] three\n"})
	code, st := e.do("GET", "/api/state", nil)
	if code != 200 || int(st["totalTasks"].(float64)) != 3 {
		t.Fatalf("state: %d %v", code, st)
	}
	before := e.read("a.txt")
	code, _ = e.do("POST", "/api/task", map[string]any{"path": "a.txt", "line": 1, "done": true, "raw": "- [ ] one !1 #x"})
	if code != 200 {
		t.Fatalf("tick: %d", code)
	}
	after := e.read("a.txt")
	if len(after) != len(before) || bytes.Count(after, []byte("x")) != bytes.Count(before, []byte("x"))+1 {
		t.Fatalf("tick changed more than the checkbox: %q -> %q", before, after)
	}
	code, out := e.do("POST", "/api/task", map[string]any{"path": "a.txt", "line": 1, "done": false, "raw": "- [ ] one !1 #x"})
	if code != 409 || out["code"] != "stale" {
		t.Fatalf("stale tick should conflict: %d %v", code, out)
	}
}

func TestBasicGetPutRoundTrip(t *testing.T) {
	e := btNew(t, map[string]string{"n.txt": "line one\r\nline two\r\n"})
	code, f := e.do("GET", "/api/file?path=n.txt", nil)
	if code != 200 || f["text"] != "line one\nline two\n" || f["eol"] != "crlf" || f["lossless"] != true {
		t.Fatalf("get: %d %v", code, f)
	}
	code, _ = e.do("PUT", "/api/file", map[string]any{"path": "n.txt", "text": "changed\nline two\n", "kind": f["kind"], "eol": f["eol"], "expectedHash": f["hash"]})
	if code != 200 {
		t.Fatalf("put: %d", code)
	}
	if got := string(e.read("n.txt")); got != "changed\r\nline two\r\n" {
		t.Fatalf("CRLF not preserved: %q", got)
	}
	// a stale expectedHash must not overwrite
	code, out := e.do("PUT", "/api/file", map[string]any{"path": "n.txt", "text": "x", "kind": "utf8", "eol": "crlf", "expectedHash": f["hash"]})
	if code != 409 || out["code"] != "changed" {
		t.Fatalf("expected a changed conflict: %d %v", code, out)
	}
	if got := string(e.read("n.txt")); got != "changed\r\nline two\r\n" {
		t.Fatalf("conflicted PUT modified the file: %q", got)
	}
}

func TestBasicCreateAndNeverOverwriteReadOnly(t *testing.T) {
	bad := "\xef\xbb\xbf- [ ] a\xff\n"
	e := btNew(t, map[string]string{"bad.txt": bad})
	code, out := e.do("PUT", "/api/file", map[string]any{"path": "work/new.txt", "text": "hi\n", "kind": "utf8", "eol": "crlf", "create": true})
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	if got := string(e.read("work/new.txt")); got != "hi\r\n" {
		t.Fatalf("created file = %q", got)
	}
	code, out = e.do("PUT", "/api/file", map[string]any{"path": "work/new.txt", "text": "again", "kind": "utf8", "eol": "crlf", "create": true})
	if code != 409 || out["code"] != "exists" {
		t.Fatalf("create over existing: %d %v", code, out)
	}
	code, out = e.do("PUT", "/api/file", map[string]any{"path": "bad.txt", "text": "oops", "kind": "utf8bom", "eol": "lf", "overwrite": true})
	if code != 409 || out["code"] != "readonly" {
		t.Fatalf("read-only file must never be overwritten: %d %v", code, out)
	}
	if string(e.read("bad.txt")) != bad {
		t.Fatal("read-only file was modified")
	}
}

func TestBasicSecurityGuards(t *testing.T) {
	e := btNew(t, map[string]string{"a.txt": "- [ ] one\n"})
	get := func(path string, hdr map[string]string) int {
		req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if v, ok := hdr["Host"]; ok {
			req.Host = v
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get("/api/state", nil); c != 401 {
		t.Errorf("no token: %d", c)
	}
	if c := get("/api/state?t="+btToken, nil); c != 401 {
		t.Errorf("token in the query must not work outside /api/events: %d", c)
	}
	if c := get("/api/state", map[string]string{"X-Trimple-Token": "wrong"}); c != 401 {
		t.Errorf("wrong token: %d", c)
	}
	if c := get("/api/state", map[string]string{"X-Trimple-Token": btToken, "Host": "evil.example"}); c != 403 {
		t.Errorf("bad Host: %d", c)
	}
	if c := get("/", map[string]string{"Host": "evil.example"}); c != 403 {
		t.Errorf("bad Host on the page itself: %d", c)
	}
	if c := get("/", nil); c != 200 {
		t.Errorf("index: %d", c)
	}
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/quit", nil)
	req.Header.Set("X-Trimple-Token", btToken)
	req.Header.Set("Origin", "https://evil.example")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("cross-origin POST: %d", resp.StatusCode)
	}
	for _, p := range []string{"../x.txt", "..\\x.txt", "/etc/passwd", "C:\\x.txt", "a/../../x.txt", "CON.txt", "a.txt:evil", "x?.txt", "dir/..", "trailing."} {
		code, _ := e.do("GET", "/api/file?path="+strings.ReplaceAll(strings.ReplaceAll(p, "\\", "%5C"), "?", "%3F"), nil)
		if code != 400 {
			t.Errorf("path %q: %d, want 400", p, code)
		}
	}
	// response headers
	resp, _ = http.Get(e.ts.URL + "/")
	resp.Body.Close()
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Cache-Control", "X-Frame-Options"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS must never be enabled")
	}
}

func TestBasicSettingsAndFolder(t *testing.T) {
	e := btNew(t, map[string]string{"a.txt": "- [ ] one\n"})
	code, out := e.do("PUT", "/api/settings", map[string]any{"folder": "/ignored", "theme": "neon", "fontSize": 9999, "wordWrap": true, "panelVisible": true, "panelWidth": 1, "groupBy": "nope", "showDone": false, "openFiles": []string{"a.txt"}})
	if code != 200 || out["theme"] != "dark" || out["fontSize"].(float64) != 14 || out["groupBy"] != "priority" {
		t.Fatalf("settings not sanitised: %d %v", code, out)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "z.txt"), []byte("- [ ] zed\n- [ ] zee\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ = e.do("POST", "/api/folder", map[string]any{"path": other}); code != 200 {
		t.Fatalf("folder switch: %d", code)
	}
	_, st := e.do("GET", "/api/state", nil)
	if int(st["totalTasks"].(float64)) != 2 {
		t.Fatalf("after switching folder: %v", st["totalTasks"])
	}
	if code, _ = e.do("POST", "/api/folder", map[string]any{"path": "relative/dir"}); code != 400 {
		t.Errorf("relative folder: %d", code)
	}
	if code, _ = e.do("POST", "/api/folder", map[string]any{"path": filepath.Join(other, "missing")}); code != 400 {
		t.Errorf("missing folder without create: %d", code)
	}
	fresh := filepath.Join(other, "Trimple Notes")
	if code, _ = e.do("POST", "/api/folder", map[string]any{"path": fresh, "create": true}); code != 200 {
		t.Fatalf("create folder: %d", code)
	}
	if _, err := os.Stat(filepath.Join(fresh, "Welcome.txt")); err != nil {
		t.Error("a freshly created folder should get Welcome.txt")
	}
}

func TestBasicNotepadAndResolve(t *testing.T) {
	e := btNew(t, map[string]string{"a.txt": "x\n"})
	var opened string
	old := OpenInNotepad
	OpenInNotepad = func(p string) error { opened = p; return nil }
	defer func() { OpenInNotepad = old }()
	if code, _ := e.do("POST", "/api/notepad", map[string]any{"path": "a.txt"}); code != 200 || filepath.Base(opened) != "a.txt" {
		t.Fatalf("notepad: %d %q", code, opened)
	}
	if code, _ := e.do("POST", "/api/notepad", map[string]any{"path": "missing.txt"}); code != 404 {
		t.Errorf("notepad on a missing file: %d", code)
	}
	if code, _ := e.do("POST", "/api/notepad", map[string]any{"path": "../x.txt"}); code != 400 {
		t.Errorf("notepad traversal: %d", code)
	}
	code, out := e.do("GET", "/api/resolve?abs="+filepath.Join(e.root, "a.txt"), nil)
	if code != 200 || out["inside"] != true || out["path"] != "a.txt" {
		t.Errorf("resolve inside: %v", out)
	}
	_, out = e.do("GET", "/api/resolve?abs="+filepath.Join(t.TempDir(), "a.txt"), nil)
	if out["inside"] != false {
		t.Errorf("resolve outside: %v", out)
	}
}

func TestBasicSSEAndAutoExit(t *testing.T) {
	e := btNew(t, map[string]string{"a.txt": "- [ ] one\n"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.srv.scanLoop(ctx) // Run() normally starts this
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/events?t="+btToken, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "event: version") {
		t.Fatalf("first event: %q", buf[:n])
	}
	// an outside edit is announced
	time.Sleep(80 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(e.root, "a.txt"), []byte("- [ ] one\n- [ ] two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		b := make([]byte, 256)
		n, _ := resp.Body.Read(b)
		done <- string(b[:n])
	}()
	select {
	case got := <-done:
		if !strings.Contains(got, "event: version") {
			t.Fatalf("expected a version event, got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event after an outside edit")
	}
}

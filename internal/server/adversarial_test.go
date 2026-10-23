package server

// Adversarial / security tests for the Trimple HTTP API.
//
// Naming: TestAdv_* assert behaviour that is (or must stay) correct. TestDefect_* assert the
// behaviour a hardened server SHOULD have but the current code does not: a failing TestDefect_*
// is a genuine defect in the app, not a wrong expectation, and is documented in its comment.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/amoranio/trimple/internal/notes"
	"github.com/amoranio/trimple/web"
)

const (
	advToken = "adv-token-0123456789abcdef"
	advPort  = 47999
)

var advHost = "127.0.0.1:" + strconv.Itoa(advPort)

// ------------------------------------------------------------------ fixtures

type advEnv struct {
	t       *testing.T
	srv     *Server
	h       http.Handler
	base    string // temp dir holding everything
	root    string // notes folder
	outside string // sibling folder that must never be touched
	cfg     string // settings file
}

const outsideSecret = "TOP SECRET OUTSIDE\r\n- [ ] outside task\r\n"

func newAdvEnv(t *testing.T, mods ...func(*Options)) *advEnv {
	t.Helper()
	base := t.TempDir()
	e := &advEnv{
		t: t, base: base,
		root:    filepath.Join(base, "notes"),
		outside: filepath.Join(base, "outside"),
		cfg:     filepath.Join(base, "cfg", "settings.json"),
	}
	for _, d := range []string{e.root, e.outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.write(filepath.Join(e.outside, "secret.txt"), []byte(outsideSecret))
	o := Options{Root: e.root, ConfigPath: e.cfg, Token: advToken, KeepAlive: true, ScanEvery: 50 * time.Millisecond}
	for _, m := range mods {
		m(&o)
	}
	srv, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	e.srv, e.h = srv, srv.Handler()
	return e
}

func (e *advEnv) write(path string, b []byte) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *advEnv) read(path string) []byte {
	e.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *advEnv) inRoot(rel string) string { return filepath.Join(e.root, filepath.FromSlash(rel)) }

type advResp struct {
	Code   int
	Header http.Header
	Body   []byte
}

func (r advResp) obj() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.Body, &m)
	return m
}

func (r advResp) code() string {
	s, _ := r.obj()["code"].(string)
	return s
}

func (r advResp) String() string {
	return fmt.Sprintf("%d %s", r.Code, strings.TrimSpace(string(r.Body)))
}

func localCtx(r *http.Request, port int) *http.Request {
	ctx := context.WithValue(r.Context(), http.LocalAddrContextKey, net.Addr(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}))
	return r.WithContext(ctx)
}

// send runs one request straight through the handler (so a panic is caught and reported).
func (e *advEnv) send(method, target string, body []byte, mods ...func(*http.Request)) (resp advResp) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	r.Host = advHost
	r.Header.Set("X-Trimple-Token", advToken)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r = localCtx(r, advPort)
	for _, m := range mods {
		m(r)
	}
	defer func() {
		if x := recover(); x != nil {
			e.t.Errorf("PANIC serving %s %s: %v\n%s", method, target, x, debug.Stack())
			resp = advResp{Code: 599}
		}
	}()
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return advResp{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
}

func (e *advEnv) get(target string, mods ...func(*http.Request)) advResp {
	return e.send(http.MethodGet, target, nil, mods...)
}

func jb(kv ...any) []byte {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return b
}

func qp(path, rel string) string { return path + "?path=" + url.QueryEscape(rel) }

func (e *advEnv) getFile(rel string) advResp { return e.get(qp("/api/file", rel)) }
func (e *advEnv) putFile(kv ...any) advResp  { return e.send(http.MethodPut, "/api/file", jb(kv...)) }

// tick sends a tick the way the real client does: with the text of the line it believes is there.
// A nil raw means "whatever the line currently says" (empty when the line or file does not exist).
func (e *advEnv) tick(rel string, line int, done bool, raw *string) advResp {
	if raw == nil {
		cur := ""
		if b, err := os.ReadFile(e.inRoot(rel)); err == nil {
			lines := strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(b)), "\n")
			if line >= 1 && line <= len(lines) {
				cur = lines[line-1]
			}
		}
		raw = &cur
	}
	return e.send(http.MethodPost, "/api/task", jb("path", rel, "line", line, "done", done, "raw", *raw))
}

// tickNoRaw omits raw entirely, which the API must refuse.
func (e *advEnv) tickNoRaw(rel string, line int, done bool) advResp {
	return e.send(http.MethodPost, "/api/task", jb("path", rel, "line", line, "done", done))
}

func noToken(r *http.Request)             { r.Header.Del("X-Trimple-Token") }
func hdr(k, v string) func(*http.Request) { return func(r *http.Request) { r.Header.Set(k, v) } }
func host(h string) func(*http.Request)   { return func(r *http.Request) { r.Host = h } }

// tree returns "relpath" -> content-hash (or "dir") for everything under dir.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			out[rel] = "dir"
		} else if d.Type()&os.ModeSymlink != 0 {
			out[rel] = "symlink"
		} else if b, err := os.ReadFile(p); err == nil {
			out[rel] = notes.Hash(b)
		}
		return nil
	})
	return out
}

func sameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("%s: tree changed: before=%v after=%v", what, before, after)
		return
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s: %q changed: before=%v after=%v", what, k, before, after)
			return
		}
	}
}

func noTmpFiles(t *testing.T, dir string) {
	t.Helper()
	for k := range tree(t, dir) {
		if strings.HasSuffix(k, ".trimple-tmp") {
			t.Errorf("leftover temp file %s", k)
		}
	}
}

func stubNotepad(t *testing.T, fn func(string) error) *[]string {
	t.Helper()
	old := OpenInNotepad
	var mu sync.Mutex
	calls := &[]string{}
	OpenInNotepad = func(p string) error {
		mu.Lock()
		*calls = append(*calls, p)
		mu.Unlock()
		return fn(p)
	}
	t.Cleanup(func() { OpenInNotepad = old })
	return calls
}

// ------------------------------------------------------------------ (a) auth

func TestAdv_AuthRequiredEverywhere(t *testing.T) {
	e := newAdvEnv(t)
	before := tree(t, e.base)
	endpoints := []struct{ method, target string }{
		{"GET", "/api/ping"}, {"GET", "/api/bootstrap"}, {"GET", "/api/state"}, {"GET", "/api/file?path=a.txt"},
		{"PUT", "/api/file"}, {"GET", "/api/stat?path=a.txt"}, {"POST", "/api/task"}, {"POST", "/api/notepad"},
		{"GET", "/api/fs"}, {"POST", "/api/folder"}, {"GET", "/api/settings"}, {"PUT", "/api/settings"},
		{"GET", "/api/events"}, {"GET", "/api/resolve?abs=/x.txt"}, {"POST", "/api/quit"},
		{"GET", "/api/nope"}, {"GET", "/api/"}, {"DELETE", "/api/file"}, {"OPTIONS", "/api/file"},
	}
	body := jb("path", "pwn.txt", "text", "x", "create", true)
	variants := map[string]func(*http.Request){
		"missing":     noToken,
		"empty":       hdr("X-Trimple-Token", ""),
		"wrong":       hdr("X-Trimple-Token", "wrong"),
		"prefix":      hdr("X-Trimple-Token", advToken[:len(advToken)-1]),
		"half":        hdr("X-Trimple-Token", advToken[:len(advToken)/2]),
		"extra":       hdr("X-Trimple-Token", advToken+"x"),
		"upper":       hdr("X-Trimple-Token", strings.ToUpper(advToken)),
		"sameLen":     hdr("X-Trimple-Token", advToken[:len(advToken)-1]+"X"),
		"bearer":      func(r *http.Request) { noToken(r); r.Header.Set("Authorization", "Bearer "+advToken) },
		"cookie":      func(r *http.Request) { noToken(r); r.Header.Set("Cookie", "t="+advToken+"; token="+advToken) },
		"otherHeader": func(r *http.Request) { noToken(r); r.Header.Set("X-Token", advToken) },
	}
	for name, mod := range variants {
		for _, ep := range endpoints {
			var b []byte
			if ep.method == "PUT" || ep.method == "POST" {
				b = body
			}
			r := e.send(ep.method, ep.target, b, mod)
			if r.Code != http.StatusUnauthorized {
				t.Errorf("%s %s %s: want 401, got %s", name, ep.method, ep.target, r)
			}
		}
	}
	sameTree(t, "unauthenticated attacks", before, tree(t, e.base))
}

func TestAdv_TokenInQueryOnlyAllowedForEvents(t *testing.T) {
	e := newAdvEnv(t)
	q := "t=" + url.QueryEscape(advToken)
	for _, ep := range []struct{ method, target string }{
		{"GET", "/api/ping?" + q}, {"GET", "/api/bootstrap?" + q}, {"GET", "/api/state?" + q},
		{"GET", "/api/file?path=a.txt&" + q}, {"GET", "/api/stat?path=a.txt&" + q}, {"GET", "/api/fs?" + q},
		{"GET", "/api/settings?" + q}, {"GET", "/api/resolve?abs=/a.txt&" + q},
		{"POST", "/api/task?" + q}, {"POST", "/api/folder?" + q}, {"PUT", "/api/file?" + q}, {"PUT", "/api/settings?" + q},
		{"POST", "/api/quit?" + q}, {"POST", "/api/notepad?" + q},
		{"POST", "/api/events?" + q + "&x=" + "1"}, // only checked for non-streaming behaviour below
	} {
		if strings.Contains(ep.target, "/api/events") {
			continue
		}
		r := e.send(ep.method, ep.target, []byte("{}"), noToken)
		if r.Code != http.StatusUnauthorized {
			t.Errorf("token in query accepted for %s %s: %s", ep.method, ep.target, r)
		}
	}
	// A wrong query token / empty query token on events is refused too.
	for _, tgt := range []string{"/api/events", "/api/events?t=", "/api/events?t=wrong", "/api/events?t=" + advToken[:5], "/api/events?T=" + advToken, "/api/events?token=" + advToken} {
		if r := e.get(tgt, noToken); r.Code != 401 {
			t.Errorf("%s: want 401, got %s", tgt, r)
		}
	}
	// A wrong header token must not fall back to a correct query token.
	if r := e.get("/api/events?t="+advToken, hdr("X-Trimple-Token", "wrong")); r.Code != 401 {
		t.Errorf("wrong header + right query: want 401, got %s", r)
	}
}

func TestAdv_ErrorBodiesDoNotLeak(t *testing.T) {
	e := newAdvEnv(t)
	r := e.get("/api/state", noToken)
	if strings.Contains(string(r.Body), e.root) || strings.Contains(string(r.Body), advToken) {
		t.Errorf("401 leaks internals: %s", r)
	}
	r = e.get("/api/ping", host("evil.com"))
	if r.Code != 403 || strings.Contains(string(r.Body), e.root) {
		t.Errorf("host reject: %s", r)
	}
}

// ------------------------------------------------------------------ (b) Host header

func TestAdv_HostGuard(t *testing.T) {
	e := newAdvEnv(t)
	p := strconv.Itoa(advPort)
	good := []string{"127.0.0.1:" + p, "localhost:" + p, "LOCALHOST:" + p, "LocalHost:" + p}
	bad := []string{
		"127.0.0.1", "localhost", "evil.com", "evil.com:" + p, "127.0.0.1.evil.com:" + p, "localhost.evil.com:" + p,
		"evil.com.127.0.0.1:" + p, "[::1]:" + p, "::1", "[::1]", "", "127.0.0.1:" + strconv.Itoa(advPort+1), "127.0.0.1:80",
		"127.0.0.1:" + p + ":" + p, " 127.0.0.1:" + p, "127.0.0.1:" + p + " ", "127.0.0.1.:" + p, "localhost.:" + p,
		"0.0.0.0:" + p, "127.1:" + p, "2130706433:" + p, "0x7f000001:" + p, "user@127.0.0.1:" + p, "127.0.0.1:" + p + "@evil.com",
		"evil.com#@127.0.0.1:" + p, "127.0.0.1:0" + p, "127.0.0.1:+" + p, "127.0.0.1:" + p + ".evil.com", "127.0.0.2:" + p,
		"127.0.0.1\x00.evil.com:" + p, "LOCALHOST.:" + p, "localhost:", ":" + p,
	}
	for _, target := range []string{"/api/ping", "/api/state", "/", "/app.js", "/api/events"} {
		for _, h := range good {
			if target == "/api/events" {
				continue // streaming; covered with a real server
			}
			if r := e.get(target, host(h)); r.Code != 200 {
				t.Errorf("Host %q %s: want 200, got %s", h, target, r)
			}
		}
		for _, h := range bad {
			if r := e.get(target, host(h)); r.Code != 403 {
				t.Errorf("Host %q %s: want 403, got %s", h, target, r)
			}
		}
	}
	// Writes with a bad Host are refused before touching disk.
	before := tree(t, e.base)
	for _, h := range bad {
		r := e.send("PUT", "/api/file", jb("path", "rebind.txt", "text", "x", "create", true), host(h))
		if r.Code != 403 {
			t.Errorf("PUT with Host %q: %s", h, r)
		}
	}
	sameTree(t, "rebinding writes", before, tree(t, e.base))
	// Fail closed when the listening port cannot be determined.
	r := e.get("/api/ping", func(r *http.Request) { *r = *r.WithContext(context.Background()) })
	if r.Code != 403 {
		t.Errorf("no local-addr context should fail closed, got %s", r)
	}
}

func rawRequest(t *testing.T, addr, req string) (*http.Response, string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(c, req); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return nil, err.Error()
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	return resp, string(b)
}

func TestAdv_HostGuardOverRealSocket(t *testing.T) {
	e := newAdvEnv(t)
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	addr := ts.Listener.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	tok := "X-Trimple-Token: " + advToken + "\r\n"
	cases := map[string]string{
		"good":                     "GET /api/ping HTTP/1.1\r\nHost: 127.0.0.1:" + port + "\r\n" + tok + "Connection: close\r\n\r\n",
		"goodLocalhost":            "GET /api/ping HTTP/1.1\r\nHost: localhost:" + port + "\r\n" + tok + "Connection: close\r\n\r\n",
		"two Hosts good first":     "GET /api/ping HTTP/1.1\r\nHost: 127.0.0.1:" + port + "\r\nHost: evil.com\r\n" + tok + "Connection: close\r\n\r\n",
		"two Hosts evil first":     "GET /api/ping HTTP/1.1\r\nHost: evil.com\r\nHost: 127.0.0.1:" + port + "\r\n" + tok + "Connection: close\r\n\r\n",
		"absolute URI evil":        "GET http://evil.com/api/ping HTTP/1.1\r\nHost: 127.0.0.1:" + port + "\r\n" + tok + "Connection: close\r\n\r\n",
		"http1.0 no host":          "GET /api/ping HTTP/1.0\r\n" + tok + "\r\n",
		"http1.1 no host":          "GET /api/ping HTTP/1.1\r\n" + tok + "Connection: close\r\n\r\n",
		"folded host":              "GET /api/ping HTTP/1.1\r\nHost: evil.com\r\n 127.0.0.1:" + port + "\r\n" + tok + "Connection: close\r\n\r\n",
		"host with port only evil": "GET /api/ping HTTP/1.1\r\nHost: evil.com:" + port + "\r\n" + tok + "Connection: close\r\n\r\n",
		"X-Forwarded-Host spoof":   "GET /api/ping HTTP/1.1\r\nHost: evil.com\r\nX-Forwarded-Host: 127.0.0.1:" + port + "\r\n" + tok + "Connection: close\r\n\r\n",
	}
	wantOK := map[string]bool{"good": true, "goodLocalhost": true}
	for name, req := range cases {
		resp, body := rawRequest(t, addr, req)
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		if wantOK[name] {
			if got != 200 {
				t.Errorf("%s: want 200, got %d %s", name, got, body)
			}
			continue
		}
		if got == 200 {
			t.Errorf("%s: request with a non-loopback/ambiguous Host was served: %s", name, body)
		}
	}
}

// ------------------------------------------------------------------ (c) Origin, CSRF, content type

func TestAdv_OriginGuardOnStateChanges(t *testing.T) {
	e := newAdvEnv(t)
	p := strconv.Itoa(advPort)
	badOrigins := []string{
		"null", "https://evil.com", "http://evil.com", "http://127.0.0.1:" + strconv.Itoa(advPort+1), "http://127.0.0.1",
		"http://127.0.0.1:" + p + ".evil.com", "http://localhost.evil.com:" + p, "http://evil.com:" + p,
		"chrome-extension://abcdefgh", "file://", "%%%", "://", "http://[::1]:" + p, "http://0.0.0.0:" + p,
		"https://127.0.0.1.evil.com:" + p, "javascript:alert(1)", "http://evil.com/127.0.0.1:" + p,
	}
	goodOrigins := []string{"http://127.0.0.1:" + p, "http://localhost:" + p, "http://LOCALHOST:" + p}
	before := tree(t, e.base)
	for _, o := range badOrigins {
		for _, ep := range []struct {
			m, t string
			b    []byte
		}{
			{"PUT", "/api/file", jb("path", "csrf.txt", "text", "pwn", "create", true)},
			{"POST", "/api/task", jb("path", "a.txt", "line", 1, "done", true)},
			{"POST", "/api/folder", jb("path", e.outside)},
			{"PUT", "/api/settings", jb("theme", "light")},
			{"POST", "/api/notepad", jb("path", "a.txt")},
			{"POST", "/api/quit", nil},
		} {
			r := e.send(ep.m, ep.t, ep.b, hdr("Origin", o))
			if r.Code != 403 || r.code() != "origin" {
				t.Errorf("Origin %q on %s %s: want 403/origin, got %s", o, ep.m, ep.t, r)
			}
		}
	}
	sameTree(t, "cross-origin writes", before, tree(t, e.base))
	select {
	case <-e.srv.quit:
		t.Fatal("cross-origin POST /api/quit stopped the server")
	default:
	}
	if e.srv.root() != e.root {
		t.Fatalf("cross-origin folder switch succeeded: %s", e.srv.root())
	}
	for _, o := range goodOrigins {
		r := e.send("PUT", "/api/file", jb("path", "ok.txt", "text", "hi", "create", true, "overwrite", true), hdr("Origin", o))
		if r.Code != 200 {
			t.Errorf("Origin %q: want 200, got %s", o, r)
		}
	}
	// No Origin header at all (curl, the launcher) is allowed, token still required.
	if r := e.putFile("path", "noorigin.txt", "text", "hi", "create", true); r.Code != 200 {
		t.Errorf("no Origin: %s", r)
	}
}

func TestAdv_GETWithForeignOriginIsNotADataPath(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("private.txt"), []byte("private"))
	for _, ep := range []string{"/api/file?path=private.txt", "/api/state", "/api/settings", "/api/bootstrap", "/api/fs", "/api/stat?path=private.txt"} {
		r := e.get(ep, noToken, hdr("Origin", "https://evil.com"), hdr("Sec-Fetch-Site", "cross-site"))
		if r.Code != 401 || strings.Contains(string(r.Body), "private") {
			t.Errorf("%s with foreign Origin and no token: %s", ep, r)
		}
	}
	// With the token a GET works regardless of Origin: a foreign page cannot add the custom header
	// without a CORS preflight, which is never approved (checked in TestAdv_NoCORSHeadersEver).
	if r := e.get("/api/file?path=private.txt", hdr("Origin", "https://evil.com")); r.Code != 200 {
		t.Logf("GET with token and foreign Origin: %s", r)
	}
}

func TestAdv_ContentTypeTricks(t *testing.T) {
	e := newAdvEnv(t)
	before := tree(t, e.base)
	body := jb("path", "ct.txt", "text", "x", "create", true)
	// Classic CSRF "simple request": text/plain JSON body, no custom header possible -> no token.
	for _, ct := range []string{"text/plain", "text/plain;charset=UTF-8", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", ""} {
		r := e.send("PUT", "/api/file", body, noToken, hdr("Content-Type", ct), hdr("Origin", "https://evil.com"))
		if r.Code != 401 {
			t.Errorf("simple-request style PUT (%q) not refused with 401: %s", ct, r)
		}
		r = e.send("POST", "/api/folder", jb("path", e.outside), noToken, hdr("Content-Type", ct))
		if r.Code != 401 {
			t.Errorf("simple-request style POST (%q) not refused with 401: %s", ct, r)
		}
	}
	sameTree(t, "simple requests", before, tree(t, e.base))
	// With the token but a foreign Origin: refused (second line of defence).
	if r := e.send("PUT", "/api/file", body, hdr("Content-Type", "text/plain"), hdr("Origin", "https://evil.com")); r.Code != 403 {
		t.Errorf("token + foreign origin + text/plain: %s", r)
	}
	sameTree(t, "token+foreign origin", before, tree(t, e.base))
	// A form-encoded body is not JSON.
	if r := e.send("PUT", "/api/file", []byte("path=ct.txt&text=x&create=true"), hdr("Content-Type", "application/x-www-form-urlencoded")); r.Code != 400 {
		t.Errorf("form body: %s", r)
	}
	// text/plain with token from a loopback origin is accepted (the server does not insist on JSON
	// content-type; the token + Origin checks are what protect it).
	if r := e.send("PUT", "/api/file", body, hdr("Content-Type", "text/plain")); r.Code != 200 {
		t.Errorf("token + text/plain: %s", r)
	}
}

func TestAdv_NoCORSHeadersEver(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("a.txt"), []byte("- [ ] a\r\n"))
	targets := []string{"/api/ping", "/api/file?path=a.txt", "/api/state", "/api/settings", "/api/nope", "/", "/app.js", "/missing", "/api/quitx"}
	for _, m := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "DELETE", "PATCH"} {
		for _, tg := range targets {
			for _, withTok := range []bool{false, true} {
				for _, origin := range []string{"", "https://evil.com", "http://127.0.0.1:" + strconv.Itoa(advPort)} {
					mods := []func(*http.Request){hdr("Access-Control-Request-Method", "PUT"), hdr("Access-Control-Request-Headers", "x-trimple-token")}
					if !withTok {
						mods = append(mods, noToken)
					}
					if origin != "" {
						mods = append(mods, hdr("Origin", origin))
					}
					if m == "POST" || m == "PUT" || m == "DELETE" || m == "PATCH" {
						// never really execute a quit
						if tg == "/api/quitx" {
							continue
						}
					}
					r := e.send(m, tg, nil, mods...)
					for k := range r.Header {
						if strings.HasPrefix(strings.ToLower(k), "access-control-") {
							t.Errorf("%s %s tok=%v origin=%q leaked CORS header %s", m, tg, withTok, origin, k)
						}
					}
					if m == "OPTIONS" && !withTok && strings.HasPrefix(tg, "/api/") && r.Code != 401 {
						t.Errorf("unauthenticated preflight to %s answered %s", tg, r)
					}
				}
			}
		}
	}
}

// ------------------------------------------------------------------ (k) methods

func TestAdv_MethodsEnforced(t *testing.T) {
	e := newAdvEnv(t)
	all := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
	allowed := map[string][]string{
		"/api/state": {"GET"}, "/api/file": {"GET", "PUT"}, "/api/stat": {"GET"}, "/api/task": {"POST"},
		"/api/notepad": {"POST"}, "/api/fs": {"GET"}, "/api/folder": {"POST"}, "/api/settings": {"GET", "PUT"},
		"/api/resolve": {"GET"}, "/api/quit": {"POST"},
	}
	stubNotepad(t, func(string) error { return nil })
	for tg, ok := range allowed {
		for _, m := range all {
			isOK := false
			for _, a := range ok {
				isOK = isOK || a == m
			}
			if isOK {
				continue
			}
			r := e.send(m, tg, []byte("{}"))
			// HEAD may legitimately be answered like GET (200, no body is sent by net/http) but never with data.
			if m == "HEAD" && r.Code == 200 {
				continue
			}
			if r.Code != 405 {
				t.Errorf("%s %s: want 405, got %s", m, tg, r)
			} else if r.Header.Get("Allow") == "" {
				t.Errorf("%s %s: 405 without Allow header", m, tg)
			}
		}
	}
	select {
	case <-e.srv.quit:
		t.Fatal("a non-POST request stopped the server")
	default:
	}
}

// ping, bootstrap and events do not check the method at all. Harmless (token still needed) but
// inconsistent with every other endpoint: POST /api/events even opens a long-lived SSE stream.
func TestDefect_PingBootstrapEventsIgnoreMethod(t *testing.T) {
	e := newAdvEnv(t)
	for _, tg := range []string{"/api/ping", "/api/bootstrap", "/api/events"} {
		for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			r := e.send(m, tg, []byte("{}"), func(r *http.Request) {
				*r = *r.WithContext(context.WithValue(ctx, http.LocalAddrContextKey, net.Addr(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: advPort})))
			})
			cancel()
			if r.Code != 405 {
				t.Errorf("DEFECT: %s %s answered %d instead of 405", m, tg, r.Code)
			}
		}
	}
}

// ------------------------------------------------------------------ (j) static files + headers

func checkSecHeaders(t *testing.T, what string, r advResp) {
	t.Helper()
	want := map[string]string{
		"X-Content-Type-Options": "nosniff", "Cache-Control": "no-store", "X-Frame-Options": "DENY",
		"Referrer-Policy": "no-referrer", "Content-Security-Policy": csp,
	}
	for k, v := range want {
		if got := r.Header.Get(k); got != v {
			t.Errorf("%s: header %s = %q, want %q", what, k, got, v)
		}
	}
	for _, frag := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'", "form-action 'none'", "connect-src 'self'"} {
		if !strings.Contains(r.Header.Get("Content-Security-Policy"), frag) {
			t.Errorf("%s: CSP lacks %s", what, frag)
		}
	}
	if strings.Contains(r.Header.Get("Content-Security-Policy"), "unsafe") {
		t.Errorf("%s: CSP contains unsafe-*", what)
	}
}

func TestAdv_SecurityHeadersOnEverything(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("a.txt"), []byte("hello"))
	checkSecHeaders(t, "static /", e.get("/", noToken))
	checkSecHeaders(t, "static /app.js", e.get("/app.js"))
	checkSecHeaders(t, "static 404", e.get("/does-not-exist"))
	checkSecHeaders(t, "api 200", e.get("/api/state"))
	checkSecHeaders(t, "api file 200", e.getFile("a.txt"))
	checkSecHeaders(t, "api 401", e.get("/api/state", noToken))
	checkSecHeaders(t, "api 400", e.getFile("../x"))
	checkSecHeaders(t, "api 405", e.send("DELETE", "/api/file", nil))
	checkSecHeaders(t, "api 403 origin", e.send("PUT", "/api/file", []byte("{}"), hdr("Origin", "https://evil.com")))
	checkSecHeaders(t, "redirect", e.get("//api/state", noToken))
	if r := e.get("/api/state"); !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Errorf("API content type: %q", r.Header.Get("Content-Type"))
	}
	if r := e.get("/", noToken); !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		t.Errorf("index content type: %q", r.Header.Get("Content-Type"))
	}
}

func TestAdv_StaticTraversalAndLeaks(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("a.txt"), []byte("NOTE-SECRET"))
	targets := []string{
		"/../", "/%2e%2e/", "/%2e%2e/%2e%2e/etc/passwd", "/..%2f..%2fetc/passwd", "/..%5c..%5cx", "/..\\..\\x", "/static/../../go.mod",
		"/embed.go", "/go.mod", "//etc/passwd", "/%00", "/.%2e/", "/index.html/../../x", "/%2e%2e%2f%2e%2e%2fgo.mod", "/....//....//etc/passwd",
		"/web/embed.go", "/internal/server/server.go", "/a.txt", "/notes/a.txt", "/.git/config", "/%252e%252e/", "/..;/x", "/%c0%ae%c0%ae/",
		"//api/state", "/./api/state", "/x/../api/state", "/api/./state", "/api//state", "/api/state/", "/API/state", "/api/file/", "/api/file%2f",
		"/api%2fstate", "/index.html", "/%69ndex.html",
	}
	for _, tg := range targets {
		r := e.get(tg, noToken)
		body := string(r.Body)
		if r.Code >= 500 {
			t.Errorf("GET %s: server error %d", tg, r.Code)
		}
		for _, bad := range []string{"package web", "package server", "root:x:", "NOTE-SECRET", "module github.com", `"folder"`, advToken} {
			if strings.Contains(body, bad) {
				t.Errorf("GET %s leaks %q (status %d)", tg, bad, r.Code)
			}
		}
		if r.Code == 200 && !strings.Contains(r.Header.Get("Content-Type"), "text/") && !strings.Contains(r.Header.Get("Content-Type"), "image/svg") && !strings.Contains(r.Header.Get("Content-Type"), "javascript") {
			t.Errorf("GET %s served 200 with %q", tg, r.Header.Get("Content-Type"))
		}
		// With the guard in front, an /api path variant must never give JSON without the token.
		if r.Code == 200 && strings.Contains(r.Header.Get("Content-Type"), "json") {
			t.Errorf("GET %s returned JSON without a token", tg)
		}
		checkSecHeaders(t, "static "+tg, r)
	}
	// Only the four embedded UI files exist.
	for _, f := range []string{"/", "/index.html", "/app.js", "/style.css", "/favicon.svg"} {
		if r := e.get(f, noToken); r.Code != 200 && r.Code != 301 {
			t.Errorf("embedded file %s: %d", f, r.Code)
		}
	}
	for _, f := range []string{"/embed.go", "/main.go", "/server.go"} {
		if r := e.get(f, noToken); r.Code != 404 {
			t.Errorf("%s should be 404, got %d", f, r.Code)
		}
	}
}

// The CSP is `default-src 'self'` with no unsafe-inline: the page must not rely on inline code.
func TestAdv_IndexHTMLIsCSPCompatibleAndTokenFree(t *testing.T) {
	b, err := web.Files.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ToLower(string(b))
	for _, bad := range []string{"<style", "style=", " onclick=", " onload=", " onerror=", "javascript:", "eval("} {
		if strings.Contains(s, bad) {
			t.Errorf("index.html contains %q, which the CSP blocks", bad)
		}
	}
	for i := strings.Index(s, "<script"); i >= 0; {
		end := strings.Index(s[i:], ">")
		if !strings.Contains(s[i:i+end], "src=") {
			t.Errorf("inline <script> in index.html")
		}
		n := strings.Index(s[i+1:], "<script")
		if n < 0 {
			break
		}
		i += n + 1
	}
	if strings.Contains(s, advToken) {
		t.Error("token embedded in static page")
	}
}

// ------------------------------------------------------------------ (d) path validation on every endpoint

type pathCase struct{ name, p string }

func evilPaths(e *advEnv) []pathCase {
	cs := []pathCase{
		{"empty", ""}, {"slash", "/"}, {"backslash", `\`}, {"space", " "}, {"dot", "."}, {"dotdot", ".."}, {"dots3", "..."},
		{"dotdot-file", "../x.txt"}, {"dotdot-bs", `..\x.txt`}, {"nested-dotdot", "a/../../x.txt"}, {"nested-dotdot-bs", `a\..\..\x.txt`},
		{"mixed-dotdot", `a/..\..\x.txt`}, {"cur-dir", "./x.txt"}, {"cur-mid", "sub/./x.txt"}, {"trailing-dotdot", "sub/.."},
		{"dotdot-sibling", "../outside/secret.txt"}, {"dotdot-sibling-bs", `..\outside\secret.txt`}, {"deep", strings.Repeat("../", 40) + "etc/passwd"},
		{"dotdot-then-root", "../notes/x.txt"},
		{"abs-unix", "/etc/passwd"}, {"abs-unix-txt", "/tmp/x.txt"}, {"abs-outside", filepath.Join(e.outside, "secret.txt")},
		{"abs-inside-root", filepath.Join(e.root, "x.txt")},
		{"unc", `\\server\share\x.txt`}, {"unc-slash", "//server/share/x.txt"}, {"unc-qm", `\\?\C:\x.txt`}, {"unc-dot", `\\.\pipe\x.txt`},
		{"mixed-lead", `/\x.txt`}, {"drive-bs", `C:\x.txt`}, {"drive-slash", "C:/x.txt"}, {"drive-rel", "C:x.txt"}, {"drive-lower", `c:\windows\x.txt`},
		{"nul", "x\x00.txt"}, {"nul-ext", "x.txt\x00.png"}, {"nul-only", "\x00"}, {"nul-dir", "sub\x00/x.txt"},
		{"long", strings.Repeat("a", 497) + ".txt"}, {"long-dirs", strings.Repeat("d/", 300) + "x.txt"},
		{"CON", "CON"}, {"con.txt", "con.txt"}, {"Con.TxT", "Con.TxT"}, {"nul.txt", "nul.txt"}, {"NUL", "NUL"}, {"prn", "PRN.txt"}, {"aux", "aux.txt"},
		{"COM1", "COM1.txt"}, {"com9", "com9.txt"}, {"LPT9.log", "LPT9.log"}, {"lpt1", "LPT1"}, {"con.txt.txt", "con.txt.txt"},
		{"dir-con", "sub/con.txt"}, {"mid-aux", "sub/aux/x.txt"}, {"con-space", "CON .txt"}, {"nul.tar.txt", "nul.tar.txt"},
		{"trailing-dot", "a.txt."}, {"trailing-space", "a.txt "}, {"dir-trailing-dot", "dir./x.txt"}, {"dir-trailing-space", "dir /x.txt"},
		{"ads", "a.txt:evil"}, {"ads-data", "a.txt::$DATA"}, {"ads-dir", "a:b/x.txt"}, {"colon-lead", ":x.txt"}, {"ads-stream-txt", "x.txt:y.txt"},
		{"star", "*.txt"}, {"qmark", "a?.txt"}, {"lt", "a<b.txt"}, {"gt", "a>b.txt"}, {"pipe", "a|b.txt"}, {"quote", `a"b.txt`},
		{"ctl-01", "a\x01.txt"}, {"ctl-1f", "a\x1f.txt"}, {"newline", "a\n.txt"}, {"cr", "a\r.txt"}, {"tab", "a\t.txt"}, {"esc", "a\x1b[0m.txt"},
	}
	return cs
}

func TestAdv_EvilPathsRejectedOnEveryEndpoint(t *testing.T) {
	e := newAdvEnv(t)
	calls := stubNotepad(t, func(string) error { return nil })
	// a real note so "ok" paths exist
	e.write(e.inRoot("a.txt"), []byte("- [ ] a\r\n"))
	baseBefore := tree(t, e.base)
	for _, c := range evilPaths(e) {
		c := c
		t.Run(fmt.Sprintf("%s", c.name), func(t *testing.T) {
			e.t = t
			if r := e.getFile(c.p); r.Code != 400 || r.code() != "bad_path" {
				t.Errorf("GET /api/file: %s", r)
			}
			if r := e.putFile("path", c.p, "text", "PWNED", "create", true, "overwrite", true); r.Code != 400 || r.code() != "bad_path" {
				t.Errorf("PUT /api/file: %s", r)
			}
			if r := e.tick(c.p, 1, true, nil); r.Code != 400 || r.code() != "bad_path" {
				t.Errorf("POST /api/task: %s", r)
			}
			if r := e.send("POST", "/api/notepad", jb("path", c.p)); r.Code != 400 || r.code() != "bad_path" {
				t.Errorf("POST /api/notepad: %s", r)
			}
			r := e.get(qp("/api/stat", c.p))
			if r.Code != 200 {
				t.Errorf("GET /api/stat: %s", r)
			} else if m, _ := r.obj()[c.p].(map[string]any); m == nil || m["exists"] != false {
				t.Errorf("stat reveals existence for evil path: %s", r)
			}
		})
	}
	e.t = t
	if len(*calls) != 0 {
		t.Errorf("OpenInNotepad called for evil paths: %v", *calls)
	}
	sameTree(t, "tree after evil paths", baseBefore, tree(t, e.base))
}

func TestAdv_EncodedTraversalInQuery(t *testing.T) {
	e := newAdvEnv(t)
	for _, raw := range []string{
		"%2e%2e%2fsecret.txt", "%2e%2e/%2e%2e/outside/secret.txt", "..%5coutside%5csecret.txt", "%2E%2E%2Foutside%2Fsecret.txt",
		"..%2Foutside%2Fsecret.txt", "%2e%2e%5c%2e%2e%5coutside%5csecret.txt", "a/%2e%2e/%2e%2e/outside/secret.txt",
	} {
		r := e.get("/api/file?path=" + raw)
		if r.Code != 400 || r.code() != "bad_path" || strings.Contains(string(r.Body), "SECRET") {
			t.Errorf("path=%s: %s", raw, r)
		}
	}
	// Double encoding / overlong UTF-8 decode to harmless literal names: never to a traversal.
	for _, raw := range []string{"%252e%252e%252foutside%252fsecret.txt", "%252e%252e%255coutside", "%c0%ae%c0%ae/outside/secret.txt", "%c0%ae%c0%ae%c0%afsecret.txt", "%e2%80%a4%e2%80%a4/secret.txt"} {
		r := e.get("/api/file?path=" + raw)
		if r.Code == 200 || strings.Contains(string(r.Body), "SECRET") {
			t.Errorf("path=%s escaped: %s", raw, r)
		}
	}
	// Percent signs in a JSON path are just characters in a file name.
	if r := e.putFile("path", "%2e%2e%2fx.txt", "text", "lit", "create", true); r.Code != 200 {
		t.Fatalf("literal percent name: %s", r)
	}
	if _, err := os.Stat(e.inRoot("%2e%2e%2fx.txt")); err != nil {
		t.Errorf("literal file not in root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.base, "x.txt")); err == nil {
		t.Error("percent-encoded traversal created a file outside the notes folder")
	}
}

// Characters that look like dots or separators but are ordinary code points on every file system.
func TestAdv_UnicodeLookalikes(t *testing.T) {
	e := newAdvEnv(t)
	outsideBefore := tree(t, e.outside)
	baseTop := func() []string {
		ents, _ := os.ReadDir(e.base)
		var n []string
		for _, x := range ents {
			n = append(n, x.Name())
		}
		sort.Strings(n)
		return n
	}
	topBefore := baseTop()
	for _, p := range []string{
		"\u2024\u2024/x.txt", "\uFF0E\uFF0E/x.txt", "a\u2215b.txt", "a\uFF0Fb.txt", "\u2025/x.txt", "\u2024\u2024\u2215x.txt",
		"\uFF0E\uFF0E\uFF0Fx.txt", "..\u2215x.txt", "\u2044x.txt", "a\u29f8b.txt", "\uFF3Cb.txt", "a\u202etxt.txt", "\u200b.txt", "e\u0301.txt",
	} {
		r := e.putFile("path", p, "text", "lookalike", "create", true)
		if r.Code != 200 && r.Code != 400 {
			t.Errorf("%q: %s", p, r)
		}
		if r.Code == 200 {
			if _, err := os.Stat(e.inRoot(p)); err != nil {
				t.Errorf("%q written but not at its literal place under the root: %v", p, err)
			}
		}
	}
	sameTree(t, "outside", outsideBefore, tree(t, e.outside))
	if got := baseTop(); strings.Join(got, ",") != strings.Join(topBefore, ",") {
		t.Errorf("new entries next to the notes folder: %v -> %v", topBefore, got)
	}
}

func TestAdv_NoFalsePositivesOnLegitNames(t *testing.T) {
	e := newAdvEnv(t)
	for _, p := range []string{
		"a.txt", "sub dir/b.txt", "ünï.txt", "日本語.txt", "console.txt", "nullable.txt", "com10.txt", "aux1.txt", "CONOUT.txt", "my.con.txt",
		"A.TXT", "x.TxT", "a b c.txt", "deep/er/est/n.txt", "sub//dbl.txt", "dash-underscore_1.txt", "dollar$.txt", "a,b;c.txt", "quote's.txt",
		"[brackets].txt", "{braces}.txt", "100%.txt", "a&b.txt", "#hash.txt", "tilde~.txt", "at@.txt", "plus+eq=.txt", "a(1).txt", "!bang.txt", "^caret.txt", "`tick`.txt",
	} {
		if r := e.putFile("path", p, "text", "ok "+p, "create", true); r.Code != 200 {
			t.Errorf("legit name %q rejected: %s", p, r)
			continue
		}
		if r := e.getFile(p); r.Code != 200 || r.obj()["text"] != "ok "+p {
			t.Errorf("legit name %q cannot be read back: %s", p, r)
		}
	}
}

// Windows reserves COM¹ COM² COM³ LPT¹ LPT² LPT³ (superscript digits) like COM1.
// isReservedName only handles the ASCII digits 1-9.
func TestDefect_WindowsSuperscriptDeviceNamesNotReserved(t *testing.T) {
	for _, n := range []string{"COM\u00b9.txt", "COM\u00b2.txt", "COM\u00b3.txt", "LPT\u00b9.txt", "LPT\u00b2", "lpt\u00b3.log"} {
		if err := validRel(n); err == nil {
			t.Errorf("DEFECT: validRel(%q) accepted a Windows reserved device name", n)
		}
	}
}

// PUT insists on .txt, but GET /api/file, /api/task and /api/notepad accept ANY file under the
// notes folder (e.g. .git/config, data.csv, tool.exe). Not an escape from the folder, but the API
// contract and the UI are .txt-only; tick would rewrite a byte in any text-like file.
func TestDefect_ExtensionAndHiddenFoldersNotEnforcedOutsidePut(t *testing.T) {
	e := newAdvEnv(t)
	calls := stubNotepad(t, func(string) error { return nil })
	e.write(e.inRoot("data.csv"), []byte("a,b\r\n1,2\r\n"))
	e.write(e.inRoot("script.bat"), []byte("echo hi\r\n- [ ] x\r\n"))
	e.write(e.inRoot("tool.exe"), []byte("MZ"))
	e.write(e.inRoot(".git/config"), []byte("[core]\n"))
	for _, p := range []string{"data.csv", ".git/config"} {
		if r := e.getFile(p); r.Code == 200 {
			t.Errorf("DEFECT: GET /api/file served non-.txt/hidden %q", p)
		}
	}
	before := e.read(e.inRoot("script.bat"))
	if r := e.tick("script.bat", 2, true, nil); r.Code == 200 {
		t.Errorf("DEFECT: POST /api/task rewrote script.bat")
	}
	if !bytes.Equal(before, e.read(e.inRoot("script.bat"))) {
		t.Errorf("DEFECT: script.bat modified")
	}
	if r := e.send("POST", "/api/notepad", jb("path", "tool.exe")); r.Code == 200 || len(*calls) != 0 {
		t.Errorf("DEFECT: /api/notepad launched an editor on tool.exe: %s", r)
	}
}

// ------------------------------------------------------------------ (d) symlinks

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestAdv_SymlinksCannotEscapeTheNotesFolder(t *testing.T) {
	e := newAdvEnv(t)
	calls := stubNotepad(t, func(string) error { return nil })
	symlinkOrSkip(t, e.outside, e.inRoot("linkdir"))                                  // dir symlink out
	symlinkOrSkip(t, filepath.Join(e.outside, "secret.txt"), e.inRoot("link.txt"))    // file symlink out
	symlinkOrSkip(t, filepath.Join(e.outside, "ghost.txt"), e.inRoot("dangling.txt")) // dangling, target not yet existing
	symlinkOrSkip(t, e.base, e.inRoot("up"))                                          // to the parent of the root
	if err := os.MkdirAll(e.inRoot("a"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, e.outside, e.inRoot("a/b")) // symlinked directory in the middle
	symlinkOrSkip(t, "../../outside", e.inRoot("a/rel"))
	symlinkOrSkip(t, e.inRoot("loop"), e.inRoot("loop"))
	symlinkOrSkip(t, "/", e.inRoot("slash"))

	before := tree(t, e.outside)
	baseBefore := map[string]bool{}
	ents, _ := os.ReadDir(e.base)
	for _, x := range ents {
		baseBefore[x.Name()] = true
	}

	paths := []string{
		"linkdir/secret.txt", "linkdir/new.txt", "linkdir/sub/deep/new.txt", "link.txt", "dangling.txt", "up/outside/secret.txt", "up/pwn.txt",
		"a/b/secret.txt", "a/b/new.txt", "a/b/x/y/new.txt", "a/rel/secret.txt", "a/rel/new2.txt", "loop/x.txt", "slash/etc/pwn.txt", "slash/tmp/pwn.txt",
	}
	for _, p := range paths {
		if r := e.getFile(p); r.Code == 200 || strings.Contains(string(r.Body), "SECRET") {
			t.Errorf("GET %s: %s", p, r)
		}
		if r := e.putFile("path", p, "text", "PWNED", "create", true, "overwrite", true); r.Code == 200 {
			t.Errorf("PUT %s succeeded", p)
		}
		if r := e.putFile("path", p, "text", "PWNED"); r.Code == 200 {
			t.Errorf("PUT (update) %s succeeded", p)
		}
		if r := e.tick(p, 2, true, nil); r.Code == 200 {
			t.Errorf("tick %s succeeded", p)
		}
		if r := e.send("POST", "/api/notepad", jb("path", p)); r.Code == 200 {
			t.Errorf("notepad %s: %s", p, r)
		}
		r := e.get(qp("/api/stat", p))
		if m, _ := r.obj()[p].(map[string]any); m != nil && m["exists"] == true {
			t.Errorf("stat reveals outside file via %s", p)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("notepad launched through symlink: %v", *calls)
	}
	sameTree(t, "outside folder", before, tree(t, e.outside))
	ents, _ = os.ReadDir(e.base)
	for _, x := range ents {
		if !baseBefore[x.Name()] {
			t.Errorf("new entry next to the notes folder: %s", x.Name())
		}
	}
	if _, err := os.Lstat("/pwn.txt"); err == nil {
		t.Error("wrote /pwn.txt")
	}
	// The index must not follow them either.
	e.srv.scanNow()
	st := e.get("/api/state")
	if strings.Contains(string(st.Body), "outside task") || strings.Contains(string(st.Body), "secret.txt") {
		t.Errorf("state lists files reached through symlinks: %s", st.Body)
	}
}

func TestAdv_SymlinkInsideRootStillWorksAndResolveIsStrict(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("real/n.txt"), []byte("- [ ] t\r\n"))
	symlinkOrSkip(t, e.inRoot("real"), e.inRoot("alias"))
	if r := e.getFile("alias/n.txt"); r.Code != 200 {
		t.Errorf("inside-root symlink read: %s", r)
	}
	if r := e.putFile("path", "alias/m.txt", "text", "x", "create", true); r.Code != 200 {
		t.Errorf("inside-root symlink write: %s", r)
	} else if _, err := os.Stat(e.inRoot("real/m.txt")); err != nil {
		t.Errorf("write through inner symlink did not land in real/: %v", err)
	}
	// resolve
	symlinkOrSkip(t, e.outside, e.inRoot("linkdir"))
	symlinkOrSkip(t, filepath.Join(e.outside, "secret.txt"), e.inRoot("link.txt"))
	for _, abs := range []string{
		filepath.Join(e.outside, "secret.txt"), e.inRoot("linkdir/secret.txt"), e.inRoot("link.txt"),
		filepath.Join(e.root, "..", "outside", "secret.txt"), filepath.Join(e.root, "real", "..", "..", "outside", "secret.txt"),
		"relative.txt", "", "\x00", "/", e.inRoot("real/n.md"), e.inRoot("real"), e.root, e.root + "-evil/x.txt", e.root + "/../notes-evil/x.txt",
	} {
		r := e.get("/api/resolve?abs=" + url.QueryEscape(abs))
		if r.Code != 200 || r.obj()["inside"] != false {
			t.Errorf("resolve(%q) should be outside: %s", abs, r)
		}
	}
	r := e.get("/api/resolve?abs=" + url.QueryEscape(e.inRoot("real/n.txt")))
	if r.obj()["inside"] != true || r.obj()["path"] != "real/n.txt" {
		t.Errorf("resolve inside: %s", r)
	}
	r = e.get("/api/resolve?abs=" + url.QueryEscape(e.inRoot("REAL/../real/N.TXT")))
	_ = r // case-insensitive FS differences: only require no crash
}

func TestAdv_NoFolderChosenRefusesPaths(t *testing.T) {
	e := newAdvEnv(t, func(o *Options) { o.Root = "" })
	if e.srv.root() != "" {
		t.Skip("a saved folder exists")
	}
	if r := e.getFile("a.txt"); r.Code != 400 {
		t.Errorf("GET without folder: %s", r)
	}
	if r := e.putFile("path", "a.txt", "text", "x", "create", true); r.Code != 400 {
		t.Errorf("PUT without folder: %s", r)
	}
	if r := e.get("/api/state"); r.Code != 200 {
		t.Errorf("state without folder: %s", r)
	}
	if r := e.get("/api/resolve?abs=" + url.QueryEscape("/tmp/x.txt")); r.obj()["inside"] != false {
		t.Errorf("resolve without folder: %s", r)
	}
}

func TestAdv_OverlongNamesDoNotCrash(t *testing.T) {
	e := newAdvEnv(t)
	for _, n := range []int{255, 256, 300, 480} {
		p := strings.Repeat("n", n) + ".txt"
		g := e.getFile(p)
		pu := e.putFile("path", p, "text", "x", "create", true)
		t.Logf("name length %d: GET %d PUT %d", n+4, g.Code, pu.Code)
		if g.Code == 200 && pu.Code != 200 {
			t.Errorf("inconsistent for length %d", n)
		}
	}
}

// ------------------------------------------------------------------ (e) PUT /api/file semantics

func TestAdv_PutCreateOverwriteConflict(t *testing.T) {
	e := newAdvEnv(t)
	p := e.inRoot("n.txt")
	// create
	r := e.putFile("path", "n.txt", "text", "one\ntwo", "kind", "utf8", "eol", "crlf", "create", true)
	if r.Code != 200 {
		t.Fatalf("create: %s", r)
	}
	if got := e.read(p); string(got) != "one\r\ntwo" {
		t.Fatalf("bytes: %q", got)
	}
	h := r.obj()["hash"].(string)
	if h != notes.Hash(e.read(p)) {
		t.Errorf("returned hash does not match file")
	}
	orig := e.read(p)
	// create over existing: exists
	if r := e.putFile("path", "n.txt", "text", "CLOBBER", "create", true); r.Code != 409 || r.code() != "exists" {
		t.Errorf("create-exists: %s", r)
	}
	if !bytes.Equal(orig, e.read(p)) {
		t.Fatal("file modified by a rejected create")
	}
	// update with wrong hash: changed
	if r := e.putFile("path", "n.txt", "text", "CLOBBER", "expectedHash", "0000000000000000"); r.Code != 409 || r.code() != "changed" {
		t.Errorf("changed: %s", r)
	}
	if r := e.putFile("path", "n.txt", "text", "CLOBBER", "expectedHash", h[:len(h)-1]); r.Code != 409 || r.code() != "changed" {
		t.Errorf("prefix hash: %s", r)
	}
	if !bytes.Equal(orig, e.read(p)) {
		t.Fatal("file modified by a conflicted update")
	}
	// external change after load -> conflict
	e.write(p, []byte("changed outside"))
	if r := e.putFile("path", "n.txt", "text", "MINE", "expectedHash", h); r.Code != 409 || r.code() != "changed" {
		t.Errorf("external change not detected: %s", r)
	}
	if string(e.read(p)) != "changed outside" {
		t.Fatal("external edit overwritten")
	}
	// overwrite flag forces it
	if r := e.putFile("path", "n.txt", "text", "MINE", "expectedHash", h, "overwrite", true, "eol", "lf"); r.Code != 200 {
		t.Errorf("overwrite: %s", r)
	}
	if string(e.read(p)) != "MINE" {
		t.Errorf("after overwrite: %q", e.read(p))
	}
	// correct hash
	h2 := notes.Hash(e.read(p))
	if r := e.putFile("path", "n.txt", "text", "THEIRS", "expectedHash", h2, "eol", "lf"); r.Code != 200 {
		t.Errorf("update w/ hash: %s", r)
	}
	// create + overwrite replaces
	if r := e.putFile("path", "n.txt", "text", "FORCED", "create", true, "overwrite", true, "eol", "lf"); r.Code != 200 || string(e.read(p)) != "FORCED" {
		t.Errorf("create+overwrite: %s %q", r, e.read(p))
	}
	noTmpFiles(t, e.root)
}

func TestAdv_PutRejections(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("keep.txt"), []byte("keep"))
	before := tree(t, e.base)
	bad := []struct {
		name string
		body string
		want int
	}{
		{"non-txt", `{"path":"a.md","text":"x","create":true}`, 400},
		{"no ext", `{"path":"a","text":"x","create":true}`, 400},
		{"exe", `{"path":"a.exe","text":"x","create":true}`, 400},
		{"txt dir with dot", `{"path":"a.txt/b","text":"x","create":true}`, 400},
		{"double ext", `{"path":"a.txt.exe","text":"x","create":true}`, 400},
		{"hidden-tmp", `{"path":"keep.txt.trimple-tmp","text":"x","create":true}`, 400},
		{"NUL text", `{"path":"nul-text.txt","text":"a\u0000b","create":true}`, 400},
		{"unknown field", `{"path":"u.txt","text":"x","create":true,"extra":1}`, 400},
		{"unknown field case", `{"path":"u.txt","text":"x","create":true,"Folder":"/"}`, 400},
		{"trailing garbage", `{"path":"g.txt","text":"x","create":true} garbage`, 400},
		{"two objects", `{"path":"g.txt","text":"x","create":true}{"path":"h.txt","text":"x","create":true}`, 400},
		{"array", `[{"path":"g.txt","text":"x"}]`, 400},
		{"string", `"hello"`, 400},
		{"null body", `null`, 400},
		{"empty body", ``, 400},
		{"truncated", `{"path":"g.txt","te`, 400},
		{"path number", `{"path":5,"text":"x"}`, 400},
		{"path array", `{"path":["a.txt"],"text":"x"}`, 400},
		{"path object", `{"path":{"a":1},"text":"x"}`, 400},
		{"text number", `{"path":"t.txt","text":123,"create":true}`, 400},
		{"text object", `{"path":"t.txt","text":{},"create":true}`, 400},
		{"create string", `{"path":"t.txt","text":"x","create":"yes"}`, 400},
		{"overwrite number", `{"path":"t.txt","text":"x","overwrite":1}`, 400},
		{"hash number", `{"path":"t.txt","text":"x","expectedHash":5}`, 400},
		{"kind number", `{"path":"t.txt","text":"x","kind":7}`, 400},
		{"bad escape", `{"path":"t.txt","text":"\q"}`, 400},
		{"deep nest unknown", `{"path":"t.txt","text":"x","pad":` + strings.Repeat("[", 200000) + strings.Repeat("]", 200000) + `}`, 400},
		{"deep nest array", strings.Repeat("[", 500000), 400},
		{"deep nest text", `{"path":"t.txt","text":` + strings.Repeat("[", 500000), 400},
		{"bom prefix", "\xef\xbb\xbf" + `{"path":"t.txt","text":"x","create":true}`, 400},
	}
	for _, c := range bad {
		r := e.send("PUT", "/api/file", []byte(c.body))
		if r.Code != c.want {
			t.Errorf("%s: want %d, got %d %.120s", c.name, c.want, r.Code, r.Body)
		}
	}
	sameTree(t, "rejected PUTs", before, tree(t, e.base))
}

// json.Decoder.More() reports false when the next byte is '}' or ']', so a body like
// {"path":...}} (trailing closing bracket) is NOT detected as trailing data.
func TestDefect_TrailingBracketAfterJSONAccepted(t *testing.T) {
	e := newAdvEnv(t)
	for _, tail := range []string{"}", "]", "}}", "]]"} {
		r := e.send("PUT", "/api/file", []byte(`{"path":"tb.txt","text":"x","create":true,"overwrite":true}`+tail))
		if r.Code == 200 {
			t.Errorf("DEFECT: body with trailing %q accepted (decode() uses dec.More())", tail)
		}
	}
}

func TestAdv_PutOversizeBody(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("big.txt"), []byte("original"))
	h := notes.Hash([]byte("original"))
	body := append([]byte(`{"path":"big.txt","expectedHash":"`+h+`","text":"`), bytes.Repeat([]byte("a"), 24<<20+10)...)
	body = append(body, []byte(`"}`)...)
	r := e.send("PUT", "/api/file", body)
	if r.Code != 400 && r.Code != 413 {
		t.Errorf("oversize body: %d", r.Code)
	}
	if string(e.read(e.inRoot("big.txt"))) != "original" {
		t.Fatal("oversize PUT modified the file")
	}
	// oversize but with an early syntax error and a huge unknown field
	r = e.send("POST", "/api/task", append([]byte(`{"pad":"`), bytes.Repeat([]byte("b"), 25<<20)...))
	if r.Code != 400 && r.Code != 413 {
		t.Errorf("oversize task body: %d", r.Code)
	}
	noTmpFiles(t, e.root)
}

// A note over notes.MaxFileBytes (5 MiB) can be saved through PUT (limit 24 MiB) but then silently
// disappears from the index (file list and task panel), so the user's note appears to vanish.
func TestDefect_PutAcceptsNoteTheIndexWillHide(t *testing.T) {
	e := newAdvEnv(t)
	big := strings.Repeat("x\n", notes.MaxFileBytes/2+1000) // > 5 MiB
	r := e.putFile("path", "huge.txt", "text", big, "create", true, "eol", "lf")
	if r.Code != 200 {
		t.Skipf("PUT refused (fine): %s", r)
	}
	st := e.get("/api/state")
	var s struct {
		Files []struct{ Path string } `json:"files"`
	}
	_ = json.Unmarshal(st.Body, &s)
	found := false
	for _, f := range s.Files {
		found = found || f.Path == "huge.txt"
	}
	if !found {
		t.Errorf("DEFECT: huge.txt (%d bytes) was saved but is not listed by /api/state (MaxFileBytes=%d)", len(big), notes.MaxFileBytes)
	}
}

func TestAdv_PutNewSubfolderAndNesting(t *testing.T) {
	e := newAdvEnv(t)
	if r := e.putFile("path", "new/sub/deep/n.txt", "text", "x", "create", true); r.Code != 200 {
		t.Fatalf("%s", r)
	}
	if _, err := os.Stat(e.inRoot("new/sub/deep/n.txt")); err != nil {
		t.Error(err)
	}
	// a file where a folder is needed
	e.write(e.inRoot("file.txt"), []byte("f"))
	if r := e.putFile("path", "file.txt/child.txt", "text", "x", "create", true); r.Code == 200 {
		t.Errorf("wrote below a regular file: %s", r)
	}
	if string(e.read(e.inRoot("file.txt"))) != "f" {
		t.Error("file.txt damaged")
	}
	// directory with a .txt name
	if err := os.Mkdir(e.inRoot("dir.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := e.putFile("path", "dir.txt", "text", "x", "create", true, "overwrite", true)
	if r.Code == 200 {
		t.Errorf("replaced a directory: %s", r)
	}
	if fi, err := os.Stat(e.inRoot("dir.txt")); err != nil || !fi.IsDir() {
		t.Error("directory dir.txt replaced")
	}
	noTmpFiles(t, e.root)
}

func TestAdv_PutInvalidKindEolAreRejected(t *testing.T) {
	e := newAdvEnv(t)
	for i, kv := range [][]any{
		{"kind", "klingon", "eol", "xx"}, {"kind", "UTF8", "eol", "CRLF"}, {"kind", "../../x", "eol", "\n"},
		{"kind", "utf16le\x00", "eol", "\r\n\r\n"}, {"kind", strings.Repeat("k", 1000), "eol", strings.Repeat("\n", 100)},
	} {
		name := fmt.Sprintf("k%d.txt", i)
		args := append([]any{"path", name, "text", "a\nb", "create", true}, kv...)
		if r := e.putFile(args...); r.Code != 400 || r.code() != "bad_request" {
			t.Errorf("%v: want 400 bad_request, got %s", kv, r)
		}
		if _, err := os.Stat(e.inRoot(name)); err == nil {
			t.Errorf("%v: a file was created", kv)
		}
	}
	// omitted kind/eol on a NEW note default to UTF-8 + Windows line endings
	if r := e.putFile("path", "new.txt", "text", "a\nb", "create", true); r.Code != 200 {
		t.Fatalf("defaults: %s", r)
	}
	if got := e.read(e.inRoot("new.txt")); string(got) != "a\r\nb" {
		t.Errorf("defaults wrote %q", got)
	}
}

type encVariant struct {
	name, kind, eol string
	raw             []byte
}

func u16(s string, be bool, bom bool) []byte {
	var out []byte
	if bom {
		if be {
			out = []byte{0xFE, 0xFF}
		} else {
			out = []byte{0xFF, 0xFE}
		}
	}
	for _, v := range utf16.Encode([]rune(s)) {
		if be {
			out = append(out, byte(v>>8), byte(v))
		} else {
			out = append(out, byte(v), byte(v>>8))
		}
	}
	return out
}

func encVariants() []encVariant {
	body := func(eol string) string { return "# t\u00e9\x7f" + eol + "- [ ] a" + eol + "- [x] b #c" + eol }
	return []encVariant{
		{"utf8-crlf", "utf8", "crlf", []byte(body("\r\n"))},
		{"utf8-lf", "utf8", "lf", []byte(body("\n"))},
		{"utf8-cr", "utf8", "cr", []byte(body("\r"))},
		{"utf8bom-crlf", "utf8bom", "crlf", append([]byte{0xEF, 0xBB, 0xBF}, body("\r\n")...)},
		{"utf8bom-lf", "utf8bom", "lf", append([]byte{0xEF, 0xBB, 0xBF}, body("\n")...)},
		{"utf16le-crlf", "utf16le", "crlf", u16(body("\r\n"), false, true)},
		{"utf16be-crlf", "utf16be", "crlf", u16(body("\r\n"), true, true)},
		{"utf16le-lf", "utf16le", "lf", u16(body("\n"), false, true)},
		{"ansi-crlf", "ansi", "crlf", []byte("caf\xe9 \x80 \x93q\x94 \x81\r\n- [ ] a\r\n")},
		{"ansi-lf", "ansi", "lf", []byte("caf\xe9 \x80\n- [ ] a\n")},
		{"empty-utf8", "utf8", "crlf", []byte{}},
		{"no-trailing-eol", "utf8", "lf", []byte("a\nb")},
	}
}

func TestAdv_PutPreservesEncodingAndLineEndings(t *testing.T) {
	for _, v := range encVariants() {
		v := v
		t.Run(v.name, func(t *testing.T) {
			e := newAdvEnv(t)
			e.write(e.inRoot("f.txt"), v.raw)
			g := e.getFile("f.txt")
			if g.Code != 200 {
				t.Fatal(g)
			}
			o := g.obj()
			if o["kind"] != v.kind || o["lossless"] != true {
				t.Fatalf("GET meta: kind=%v eol=%v lossless=%v", o["kind"], o["eol"], o["lossless"])
			}
			if len(v.raw) > 0 && strings.Contains(v.name, "crlf") || strings.Contains(v.name, "-lf") && o["eol"] != v.eol {
				if o["eol"] != v.eol {
					t.Fatalf("eol=%v want %v", o["eol"], v.eol)
				}
			}
			text := o["text"].(string)
			if strings.Contains(text, "\r") {
				t.Fatalf("GET text not normalised: %q", text)
			}
			hash := o["hash"].(string)
			// no-op save: same bytes
			r := e.putFile("path", "f.txt", "text", text, "kind", o["kind"], "eol", o["eol"], "expectedHash", hash)
			if r.Code != 200 {
				t.Fatalf("no-op PUT: %s", r)
			}
			if !bytes.Equal(e.read(e.inRoot("f.txt")), v.raw) {
				t.Fatalf("no-op save changed bytes:\n got %q\nwant %q", e.read(e.inRoot("f.txt")), v.raw)
			}
			// edited save keeps encoding
			newText := text + "- [ ] added \u00e9\n"
			r = e.putFile("path", "f.txt", "text", newText, "kind", o["kind"], "eol", o["eol"], "expectedHash", hash)
			if r.Code != 200 {
				t.Fatalf("edit PUT: %s", r)
			}
			eol := eolValues[v.eol]
			expText := strings.ReplaceAll(newText, "\n", eol)
			var exp []byte
			switch v.kind {
			case "utf8":
				exp = []byte(expText)
			case "utf8bom":
				exp = append([]byte{0xEF, 0xBB, 0xBF}, expText...)
			case "utf16le":
				exp = u16(expText, false, true)
			case "utf16be":
				exp = u16(expText, true, true)
			case "ansi":
				exp = []byte(strings.ReplaceAll(expText, "\u00e9", "\xe9"))
				// text round-tripped through cp1252: map the C1 code points back
				exp = []byte(func() string {
					var sb strings.Builder
					for _, ru := range expText {
						switch {
						case ru < 0x80 || (ru >= 0xA0 && ru <= 0xFF):
							sb.WriteByte(byte(ru))
						case ru == 0x20AC:
							sb.WriteByte(0x80)
						case ru == 0x201C:
							sb.WriteByte(0x93)
						case ru == 0x201D:
							sb.WriteByte(0x94)
						case ru == 0x81:
							sb.WriteByte(0x81)
						default:
							t.Fatalf("unmapped %U", ru)
						}
					}
					return sb.String()
				}())
			}
			if got := e.read(e.inRoot("f.txt")); !bytes.Equal(got, exp) {
				t.Errorf("edited save bytes:\n got %q\nwant %q", got, exp)
			}
			g2 := e.getFile("f.txt").obj()
			if g2["kind"] != v.kind || g2["lossless"] != true {
				t.Errorf("after save kind=%v lossless=%v", g2["kind"], g2["lossless"])
			}
			noTmpFiles(t, e.root)
		})
	}
}

func TestAdv_AnsiUpgradeNeedsConsent(t *testing.T) {
	e := newAdvEnv(t)
	orig := []byte("caf\xe9\r\n")
	e.write(e.inRoot("old.txt"), orig)
	g := e.getFile("old.txt").obj()
	h := g["hash"].(string)
	r := e.putFile("path", "old.txt", "text", "caf\u00e9 \u65e5\u672c\n", "kind", "ansi", "eol", "crlf", "expectedHash", h)
	if r.Code != 409 || r.code() != "encoding" {
		t.Fatalf("want 409 encoding, got %s", r)
	}
	if !bytes.Equal(e.read(e.inRoot("old.txt")), orig) {
		t.Fatal("refused upgrade modified the file")
	}
	// emoji (outside BMP) and euro in ANSI: euro fits, emoji does not
	if r := e.putFile("path", "old.txt", "text", "\u20ac\n", "kind", "ansi", "eol", "crlf", "expectedHash", h); r.Code != 200 {
		t.Errorf("euro fits cp1252: %s", r)
	}
	h = notes.Hash(e.read(e.inRoot("old.txt")))
	if r := e.putFile("path", "old.txt", "text", "\U0001F600\n", "kind", "ansi", "eol", "crlf", "expectedHash", h); r.Code != 409 || r.code() != "encoding" {
		t.Errorf("emoji: %s", r)
	}
	r = e.putFile("path", "old.txt", "text", "caf\u00e9 \u65e5\u672c\n", "kind", "ansi", "eol", "crlf", "expectedHash", h, "allowUpgrade", true)
	if r.Code != 200 || r.obj()["kind"] != "utf8" {
		t.Fatalf("allowUpgrade: %s", r)
	}
	if got := string(e.read(e.inRoot("old.txt"))); got != "caf\u00e9 \u65e5\u672c\r\n" {
		t.Errorf("upgraded bytes %q", got)
	}
	// allowUpgrade does not weaken the other guards
	e.write(e.inRoot("old2.txt"), orig)
	if r := e.putFile("path", "old2.txt", "text", "x", "kind", "ansi", "expectedHash", "bad", "allowUpgrade", true); r.Code != 409 || r.code() != "changed" {
		t.Errorf("allowUpgrade bypassed hash check: %s", r)
	}
}

func TestAdv_NonLosslessFilesAreNeverOverwritten(t *testing.T) {
	e := newAdvEnv(t)
	files := map[string][]byte{
		"nulbyte.txt":        []byte("a\x00b\r\n- [ ] t\r\n"),
		"utf16-odd.txt":      {0xFF, 0xFE, 'a', 0, 'b'},
		"utf16-lone.txt":     {0xFF, 0xFE, 0x00, 0xD8, 'a', 0},
		"utf16be-lone.txt":   {0xFE, 0xFF, 0xDC, 0x00},
		"bom-bad.txt":        {0xEF, 0xBB, 0xBF, 'a', 0xFF},
		"ansi-nul.txt":       {'c', 'a', 'f', 0xE9, 0x00},
		"utf16-nul-char.txt": u16("a\x00b", false, true),
	}
	for name, raw := range files {
		e.write(e.inRoot(name), raw)
		g := e.getFile(name)
		if g.Code != 200 || g.obj()["lossless"] != false {
			t.Errorf("%s: GET %s", name, g)
		}
		if tx, _ := g.obj()["text"].(string); strings.ContainsRune(tx, 0) {
			t.Errorf("%s: NUL leaked in text", name)
		}
		hash := notes.Hash(raw)
		for _, kv := range [][]any{
			{"create", true, "overwrite", true},
			{"create", true},
			{"overwrite", true},
			{"expectedHash", hash},
			{"expectedHash", hash, "overwrite", true, "allowUpgrade", true},
			{},
		} {
			args := append([]any{"path", name, "text", "REPLACED", "kind", "utf8", "eol", "crlf"}, kv...)
			r := e.putFile(args...)
			createOnly := len(kv) == 2 && kv[0] == "create" // "exists" is checked first and is equally safe
			if r.Code != 409 || (r.code() != "readonly" && !(createOnly && r.code() == "exists")) {
				t.Errorf("%s %v: want 409 readonly, got %s", name, kv, r)
			}
		}
		if r := e.tick(name, 1, true, nil); r.Code == 200 {
			t.Errorf("%s: tick succeeded on non-lossless file", name)
		}
		if !bytes.Equal(e.read(e.inRoot(name)), raw) {
			t.Errorf("%s: bytes changed", name)
		}
	}
	noTmpFiles(t, e.root)
}

// A PUT that omits "text" (or sends null) is decoded as the empty string and silently truncates
// the note; a PUT for an existing file with create=false and no expectedHash skips the conflict
// check entirely. Both are data-loss hazards for a buggy or hostile (token-holding) client.
func TestDefect_PutWithoutTextOrHashDestroysExistingNote(t *testing.T) {
	e := newAdvEnv(t)
	orig := []byte("precious note\r\n- [ ] task\r\n")
	for _, c := range []struct{ name, body string }{
		{"text omitted", `{"path":"p.txt"}`},
		{"text null", `{"path":"p.txt","text":null}`},
		{"text omitted with hash", `{"path":"p.txt","expectedHash":"` + notes.Hash(orig) + `"}`},
	} {
		e.write(e.inRoot("p.txt"), orig)
		r := e.send("PUT", "/api/file", []byte(c.body))
		if r.Code == 200 && len(e.read(e.inRoot("p.txt"))) == 0 {
			t.Errorf("DEFECT: %s: note truncated to 0 bytes by %s", c.name, c.body)
		}
	}
	e.write(e.inRoot("p.txt"), orig)
	r := e.putFile("path", "p.txt", "text", "blind overwrite")
	if r.Code == 200 {
		t.Errorf("DEFECT: PUT with create=false and no expectedHash silently replaced an existing note (no conflict detection)")
	}
}

// The client chooses kind/eol; when it omits them (or sends garbage) the server falls back to
// UTF-8/CRLF even for an existing file, silently re-encoding a UTF-16/ANSI/BOM/LF note.
// For an existing file the safe fallback is the file's own kind/eol.
func TestDefect_PutOmittingKindReencodesExistingFile(t *testing.T) {
	for _, v := range encVariants() {
		if v.kind == "utf8" && v.eol == "crlf" {
			continue
		}
		if len(v.raw) == 0 {
			continue
		}
		v := v
		t.Run(v.name, func(t *testing.T) {
			e := newAdvEnv(t)
			e.write(e.inRoot("f.txt"), v.raw)
			g := e.getFile("f.txt").obj()
			r := e.putFile("path", "f.txt", "text", g["text"], "expectedHash", g["hash"], "kind", "bogus", "eol", "bogus")
			if r.Code != 200 {
				return // refusing is fine too
			}
			if !bytes.Equal(e.read(e.inRoot("f.txt")), v.raw) {
				t.Errorf("DEFECT: unchanged text with missing/invalid kind+eol re-encoded the %s file:\n got %q\nwant %q", v.name, e.read(e.inRoot("f.txt")), v.raw)
			}
		})
	}
}

func TestAdv_PutGetHashesAgree(t *testing.T) {
	e := newAdvEnv(t)
	r := e.putFile("path", "h.txt", "text", "a\nb\n", "create", true, "eol", "lf")
	g := e.getFile("h.txt").obj()
	if g["hash"] != r.obj()["hash"] || g["stamp"] != r.obj()["stamp"] {
		t.Errorf("PUT hash/stamp %v/%v differ from GET %v/%v", r.obj()["hash"], r.obj()["stamp"], g["hash"], g["stamp"])
	}
}

func TestDefect_PutConcurrentSameFileSharesTempFile(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("c.txt"), []byte("start"))
	const n = 12
	payloads := make([]string, n)
	for i := range payloads {
		payloads[i] = strings.Repeat(fmt.Sprintf("%02d-payload-line\n", i), 200*(i+1))
	}
	corrupt, failed := 0, 0
	for round := 0; round < 25; round++ {
		var wg sync.WaitGroup
		start := make(chan struct{})
		codes := make([]int, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				codes[i] = e.putFile("path", "c.txt", "text", payloads[i], "kind", "utf8", "eol", "lf", "overwrite", true).Code
			}(i)
		}
		close(start)
		wg.Wait()
		got := string(e.read(e.inRoot("c.txt")))
		ok := false
		for _, p := range payloads {
			ok = ok || got == p
		}
		if !ok {
			corrupt++
		}
		for _, c := range codes {
			if c != 200 {
				failed++
			}
		}
	}
	if corrupt > 0 {
		t.Errorf("DEFECT: in %d/25 rounds the final file equals none of the submitted payloads (interleaved bytes): concurrent writers share one fixed temp file <path>.trimple-tmp", corrupt)
	}
	if failed > 0 {
		t.Errorf("DEFECT: %d concurrent PUTs failed with a 5xx (rename of the shared .trimple-tmp raced)", failed)
	}
	noTmpFiles(t, e.root)
}

// ------------------------------------------------------------------ (f) POST /api/task

func diffCount(a, b []byte) int {
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

func TestAdv_TaskTickChangesExactlyOneByte(t *testing.T) {
	for _, v := range encVariants() {
		if !strings.Contains(string(notes.NormalizeNewlines(string(v.raw))), "[ ]") && v.kind != "utf16le" && v.kind != "utf16be" {
			continue
		}
		v := v
		t.Run(v.name, func(t *testing.T) {
			e := newAdvEnv(t)
			e.write(e.inRoot("t.txt"), v.raw)
			snap := e.getFile("t.txt").obj()
			text := snap["text"].(string)
			lines := strings.Split(text, "\n")
			line := 0
			for i, l := range lines {
				if strings.HasPrefix(l, "- [ ]") {
					line = i + 1
					raw := l
					before := e.read(e.inRoot("t.txt"))
					if r := e.tick("t.txt", line, true, &raw); r.Code != 200 {
						t.Fatalf("tick: %s", r)
					}
					after := e.read(e.inRoot("t.txt"))
					if diffCount(before, after) != 1 {
						t.Errorf("tick changed %d bytes (len %d->%d)", diffCount(before, after), len(before), len(after))
					}
					// and back
					raw2 := strings.Replace(raw, "[ ]", "[x]", 1)
					if r := e.tick("t.txt", line, false, &raw2); r.Code != 200 {
						t.Fatalf("untick: %s", r)
					}
					if !bytes.Equal(e.read(e.inRoot("t.txt")), v.raw) {
						t.Errorf("tick+untick not byte-identical")
					}
					break
				}
			}
			if line == 0 {
				t.Skip("no task in variant")
			}
			noTmpFiles(t, e.root)
		})
	}
}

func TestAdv_TaskRejections(t *testing.T) {
	e := newAdvEnv(t)
	orig := []byte("intro\r\n- [ ] one\r\n- [x] two\r\nplain\r\n")
	e.write(e.inRoot("t.txt"), orig)
	raw := func(s string) *string { return &s }
	cases := []struct {
		name string
		line int
		raw  *string
		want int
		code string
	}{
		{"stale raw", 2, raw("- [ ] other"), 409, "stale"},
		{"stale raw whitespace", 2, raw("- [ ] one "), 409, "stale"},
		{"stale raw CR left", 2, raw("- [ ] one\r"), 409, "stale"},
		{"line 0", 0, nil, 409, "stale"}, {"line -1", -1, nil, 409, "stale"}, {"line past end", 99, nil, 409, "stale"},
		{"line huge", 1 << 40, nil, 409, "stale"}, {"line minint", -1 << 62, nil, 409, "stale"},
		{"not a task", 1, nil, 409, "stale"}, {"plain line", 4, nil, 409, "stale"}, {"last empty line", 5, nil, 409, "stale"},
	}
	for _, c := range cases {
		r := e.tick("t.txt", c.line, true, c.raw)
		if r.Code != c.want || r.code() != c.code {
			t.Errorf("%s: want %d/%s got %s", c.name, c.want, c.code, r)
		}
	}
	if !bytes.Equal(e.read(e.inRoot("t.txt")), orig) {
		t.Fatal("rejected ticks modified the file")
	}
	for _, body := range []string{
		`{"path":"t.txt","line":"2","done":true}`, `{"path":"t.txt","line":2.5,"done":true}`, `{"path":"t.txt","line":1e100,"done":true}`,
		`{"path":"t.txt","line":2,"done":"yes"}`, `{"path":"t.txt","line":2,"done":true,"raw":5}`, `{"path":"t.txt","line":2,"done":true,"x":1}`,
		`{"path":"t.txt","line":99999999999999999999,"done":true}`, `{"path":"t.txt","line":null,"done":null,"raw":null} x`, `[]`,
	} {
		if r := e.send("POST", "/api/task", []byte(body)); r.Code != 400 {
			t.Errorf("%s: want 400, got %s", body, r)
		}
	}
	if r := e.tick("missing.txt", 1, true, nil); r.Code != 404 || r.code() != "missing" {
		t.Errorf("missing file: %s", r)
	}
	// done flag both ways, idempotent
	for i := 0; i < 2; i++ {
		if r := e.tick("t.txt", 2, true, nil); r.Code != 200 {
			t.Errorf("tick true #%d: %s", i, r)
		}
	}
	if got := string(e.read(e.inRoot("t.txt"))); got != "intro\r\n- [x] one\r\n- [x] two\r\nplain\r\n" {
		t.Errorf("after ticks: %q", got)
	}
	for i := 0; i < 2; i++ {
		if r := e.tick("t.txt", 3, false, nil); r.Code != 200 {
			t.Errorf("untick #%d: %s", i, r)
		}
	}
	if got := string(e.read(e.inRoot("t.txt"))); got != "intro\r\n- [x] one\r\n- [ ] two\r\nplain\r\n" {
		t.Errorf("after unticks: %q", got)
	}
	// the file moved on disk: a stale line number with the old raw is refused
	e.write(e.inRoot("t.txt"), []byte("new first line\r\nintro\r\n- [x] one\r\n- [ ] two\r\n"))
	if r := e.tick("t.txt", 2, true, raw("- [x] one")); r.Code != 409 {
		t.Errorf("moved line not detected: %s", r)
	}
	// non-lossless files and directories
	e.write(e.inRoot("bad.txt"), []byte("- [ ] a\x00\r\n"))
	if r := e.tick("bad.txt", 1, true, nil); r.Code != 409 || r.code() != "readonly" {
		t.Errorf("NUL file: %s", r)
	}
	e.write(e.inRoot("odd.txt"), []byte{0xFF, 0xFE, '-', 0, ' ', 0, '[', 0, ' ', 0, ']', 0, ' ', 0, 'x'})
	if r := e.tick("odd.txt", 1, true, nil); r.Code != 409 || r.code() != "readonly" {
		t.Errorf("odd UTF-16 file: %s", r)
	}
	noTmpFiles(t, e.root)
}

// "raw" is optional in /api/task. Without it the server ticks whatever sits on that line number,
// so a client with a stale view (file edited meanwhile) flips the WRONG task.
func TestDefect_TaskRawIsOptional(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("t.txt"), []byte("- [ ] was line 1\r\n"))
	// file changes on disk: a new task is inserted above
	e.write(e.inRoot("t.txt"), []byte("- [ ] inserted\r\n- [ ] was line 1\r\n"))
	r := e.tickNoRaw("t.txt", 1, true) // stale client meant "was line 1" but it is now line 2
	if r.Code == 200 {
		t.Errorf("DEFECT: tick without raw is applied by line number alone and ticked the wrong task: %q", e.read(e.inRoot("t.txt")))
	}
}

// Read-modify-write without any lock: two concurrent ticks of different lines of one file can both
// read the old content, and the second rename drops the first tick. Also all writers share one
// fixed temp name (<file>.trimple-tmp), so concurrent writers can fail or interleave.
func TestDefect_ConcurrentTicksLoseUpdates(t *testing.T) {
	e := newAdvEnv(t)
	const n = 20
	lostRounds, errRounds := 0, 0
	var sample string
	for round := 0; round < 30; round++ {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&sb, "- [ ] task %02d\r\n", i)
		}
		e.write(e.inRoot("many.txt"), []byte(sb.String()))
		var wg sync.WaitGroup
		start := make(chan struct{})
		var failures int32
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				raw := fmt.Sprintf("- [ ] task %02d", i)
				<-start
				if r := e.tick("many.txt", i+1, true, &raw); r.Code != 200 {
					atomic.AddInt32(&failures, 1)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		got := string(e.read(e.inRoot("many.txt")))
		if strings.Count(got, "\r\n") != n {
			t.Fatalf("file structure damaged: %q", got)
		}
		if c := strings.Count(got, "[x]"); c != n {
			lostRounds++
			if sample == "" {
				sample = fmt.Sprintf("%d of %d ticks survived (failures reported: %d)", c, n, failures)
			}
		}
		if failures > 0 {
			errRounds++
		}
	}
	noTmpFiles(t, e.root)
	if lostRounds > 0 || errRounds > 0 {
		t.Errorf("DEFECT: concurrent ticks of different lines lost updates in %d/30 rounds (%s) and returned errors in %d/30 rounds; add a per-file (or global) mutex around read-modify-write in handleTask/putFile", lostRounds, sample, errRounds)
	}
}

// Same hazard between an editor save and a task tick on the same file.
func TestDefect_TickRacingWithSaveLosesTheSave(t *testing.T) {
	e := newAdvEnv(t)
	lost, conflicts := 0, 0
	const rounds = 60
	for i := 0; i < rounds; i++ {
		e.write(e.inRoot("r.txt"), []byte("- [ ] a\r\n- [ ] b\r\n"))
		h := notes.Hash(e.read(e.inRoot("r.txt")))
		var wg sync.WaitGroup
		start := make(chan struct{})
		var tickRes, putRes advResp
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			tickRes = e.tick("r.txt", 1, true, nil)
		}()
		go func() {
			defer wg.Done()
			<-start
			putRes = e.putFile("path", "r.txt", "text", "- [ ] a\n- [ ] b\nEDIT\n", "kind", "utf8", "eol", "crlf", "expectedHash", h)
		}()
		close(start)
		wg.Wait()
		got := string(e.read(e.inRoot("r.txt")))
		// A change the server reported as successful must be in the file. A save that lost the race is
		// refused with a 409 "changed" conflict, which tells the user; that is not a silent loss.
		if tickRes.Code == 200 && !strings.Contains(got, "[x] a") {
			lost++
		}
		if putRes.Code == 200 && !strings.Contains(got, "EDIT") {
			lost++
		}
		if putRes.Code == 409 && putRes.code() == "changed" {
			conflicts++
		} else if putRes.Code != 200 {
			t.Errorf("save: unexpected %s", putRes)
		}
		if tickRes.Code != 200 {
			t.Errorf("tick: unexpected %s", tickRes)
		}
	}
	if lost > 0 {
		t.Errorf("DEFECT: %d/%d racing save+tick pairs lost a change that had been reported as saved", lost, rounds)
	}
	t.Logf("%d of %d saves lost the race and were refused with a conflict (as designed)", conflicts, rounds)
}

// ------------------------------------------------------------------ misc read-side endpoints

func TestAdv_StatEndpoint(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("s.txt"), []byte("x"))
	if err := os.Mkdir(e.inRoot("d.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := e.get("/api/stat?path=s.txt&path=nope.txt&path=d.txt&path=../x&path=" + url.QueryEscape(strings.Repeat("a", 600)))
	m := r.obj()
	if m["s.txt"].(map[string]any)["exists"] != true || m["nope.txt"].(map[string]any)["exists"] != false || m["d.txt"].(map[string]any)["exists"] != false || m["../x"].(map[string]any)["exists"] != false {
		t.Errorf("stat: %s", r)
	}
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("path=" + strconv.Itoa(i) + ".txt&")
	}
	r = e.get("/api/stat?" + sb.String())
	if len(r.obj()) > 60 {
		t.Errorf("stat not capped: %d entries", len(r.obj()))
	}
	if r := e.get("/api/stat"); r.Code != 200 || string(bytes.TrimSpace(r.Body)) != "{}" {
		t.Errorf("stat without paths: %s", r)
	}
}

func TestAdv_StateQueryFuzzDoesNotPanic(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("a.txt"), []byte("- [ ] x #l !1\r\n- [x] y\r\n"))
	e.srv.scanNow()
	for _, q := range []string{
		"", "?label=", "?label=%00", "?q=" + strings.Repeat("a", 100000), "?group=zzz", "?group=file&done=1", "?done=maybe", "?q=%ff%fe", "?q=(((", "?q=.*&label=[",
		"?group=label&done=1&q=x&label=l", "?label=" + strings.Repeat("%e2%80%ae", 1000),
	} {
		if r := e.get("/api/state" + q); r.Code != 200 {
			t.Errorf("state%.60s: %s", q, r)
		}
	}
}

func TestAdv_NotepadStub(t *testing.T) {
	e := newAdvEnv(t)
	var fail error
	calls := stubNotepad(t, func(string) error { return fail })
	e.write(e.inRoot("n.txt"), []byte("x"))
	if r := e.send("POST", "/api/notepad", jb("path", "n.txt")); r.Code != 200 {
		t.Errorf("%s", r)
	}
	if len(*calls) != 1 || (*calls)[0] != e.inRoot("n.txt") {
		t.Errorf("calls=%v want [%s]", *calls, e.inRoot("n.txt"))
	}
	if r := e.send("POST", "/api/notepad", jb("path", "missing.txt")); r.Code != 404 || len(*calls) != 1 {
		t.Errorf("missing: %s calls=%v", r, *calls)
	}
	fail = errNotepadUnsupported
	if r := e.send("POST", "/api/notepad", jb("path", "n.txt")); r.Code != 501 {
		t.Errorf("unsupported: %s", r)
	}
	fail = fmt.Errorf("boom")
	if r := e.send("POST", "/api/notepad", jb("path", "n.txt")); r.Code != 500 {
		t.Errorf("failure: %s", r)
	}
	if r := e.send("POST", "/api/notepad", jb("path", "n.txt", "args", "/p")); r.Code != 400 {
		t.Errorf("unknown field: %s", r)
	}
}

// Reading a directory named *.txt is a permanent error but ReadFile retries it like a transient
// sharing violation (6 tries, ~1.5 s of sleeping) before reporting it.
func TestDefect_PermanentReadErrorsAreRetried(t *testing.T) {
	e := newAdvEnv(t)
	if err := os.Mkdir(e.inRoot("d.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	r := e.getFile("d.txt")
	el := time.Since(t0)
	if r.Code == 200 {
		t.Fatalf("directory served as a file: %s", r)
	}
	if el > 500*time.Millisecond {
		t.Errorf("DEFECT: GET of a directory took %v (status %d): permanent errors are retried with sleeps (fileio.go retry)", el, r.Code)
	}
}

// ------------------------------------------------------------------ (g) /api/folder and /api/fs

func TestAdv_FolderEndpoint(t *testing.T) {
	e := newAdvEnv(t)
	e.write(e.inRoot("keep.txt"), []byte("k"))
	for _, p := range []string{"", "notes", "./notes", "../outside", "~/x", "\x00", "/abs/with\x00nul", "relative/path", `C:`, " "} {
		for _, create := range []bool{false, true} {
			if filepath.IsAbs(p) && p != "" {
				continue
			}
			r := e.send("POST", "/api/folder", jb("path", p, "create", create))
			if r.Code != 400 || r.code() != "bad_path" {
				t.Errorf("folder %q create=%v: %s", p, create, r)
			}
		}
	}
	if e.srv.root() != e.root {
		t.Fatalf("root changed to %q", e.srv.root())
	}
	missing := filepath.Join(e.base, "no", "such")
	if r := e.send("POST", "/api/folder", jb("path", missing)); r.Code != 400 {
		t.Errorf("missing folder, create=false: %s", r)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("folder created without create:true")
	}
	if r := e.send("POST", "/api/folder", jb("path", filepath.Join(e.outside, "secret.txt"))); r.Code != 400 {
		t.Errorf("file as folder: %s", r)
	}
	if r := e.send("POST", "/api/folder", jb("path", filepath.Join(e.outside, "secret.txt"), "create", true)); r.Code != 400 {
		t.Errorf("file as folder, create: %s", r)
	}
	if string(e.read(filepath.Join(e.outside, "secret.txt"))) != outsideSecret {
		t.Fatal("secret.txt modified")
	}
	if r := e.send("POST", "/api/folder", jb("path", missing, "create", true, "extra", 1)); r.Code != 400 {
		t.Errorf("unknown field: %s", r)
	}
	// create:true seeds Welcome.txt exactly once
	fresh := filepath.Join(e.base, "x", "y", "fresh")
	r := e.send("POST", "/api/folder", jb("path", fresh+string(filepath.Separator)+".."+string(filepath.Separator)+"fresh", "create", true))
	if r.Code != 200 || r.obj()["folder"] != fresh {
		t.Fatalf("create: %s", r)
	}
	if string(e.read(filepath.Join(fresh, "Welcome.txt"))) != welcomeText {
		t.Error("Welcome.txt not seeded")
	}
	if e.srv.root() != fresh {
		t.Errorf("root=%q", e.srv.root())
	}
	st := e.get("/api/state")
	if !strings.Contains(string(st.Body), "Welcome.txt") || strings.Contains(string(st.Body), "keep.txt") {
		t.Errorf("state after switch: %.300s", st.Body)
	}
	// existing folder: no seeding and an existing Welcome.txt is untouched
	e.write(filepath.Join(fresh, "Welcome.txt"), []byte("MINE"))
	if r := e.send("POST", "/api/folder", jb("path", fresh, "create", true)); r.Code != 200 {
		t.Fatalf("%s", r)
	}
	if string(e.read(filepath.Join(fresh, "Welcome.txt"))) != "MINE" {
		t.Error("existing Welcome.txt overwritten")
	}
	other := filepath.Join(e.base, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := e.send("POST", "/api/folder", jb("path", other, "create", true)); r.Code != 200 {
		t.Fatalf("%s", r)
	}
	if _, err := os.Stat(filepath.Join(other, "Welcome.txt")); err == nil {
		t.Error("existing empty folder was seeded with Welcome.txt")
	}
	// persisted
	var saved map[string]any
	_ = json.Unmarshal(e.read(e.cfg), &saved)
	if saved["folder"] != other {
		t.Errorf("settings file folder=%v want %s", saved["folder"], other)
	}
	// old paths no longer resolve against the new root
	if r := e.getFile("keep.txt"); r.Code != 404 {
		t.Errorf("old file visible after switch: %s", r)
	}
	// symlink pointing the notes folder elsewhere is the user's own choice; the guard still applies
	symlinkOrSkip(t, e.outside, filepath.Join(e.base, "rootlink"))
	if r := e.send("POST", "/api/folder", jb("path", filepath.Join(e.base, "rootlink"))); r.Code != 200 {
		t.Errorf("symlinked root: %s", r)
	}
	if r := e.getFile("secret.txt"); r.Code != 200 {
		t.Errorf("symlinked root should work: %s", r)
	}
	if r := e.getFile("../notes/keep.txt"); r.Code != 400 {
		t.Errorf("traversal from symlinked root: %s", r)
	}
}

func TestAdv_FSEndpoint(t *testing.T) {
	e := newAdvEnv(t)
	for _, d := range []string{"alpha", "beta", ".hidden"} {
		if err := os.Mkdir(filepath.Join(e.base, "browse-"+d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.write(filepath.Join(e.base, "browse-alpha", "file.txt"), []byte("x"))
	if err := os.Mkdir(filepath.Join(e.base, "browse-alpha", ".dot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(e.base, "browse-alpha", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := e.get("/api/fs?path=" + url.QueryEscape(filepath.Join(e.base, "browse-alpha")))
	o := r.obj()
	dirs, _ := o["dirs"].([]any)
	if r.Code != 200 || len(dirs) != 1 || dirs[0] != "sub" || o["parent"] != e.base {
		t.Errorf("listing: %s", r)
	}
	for _, p := range []string{"relative", "..", "./x", "a/b", "\x00", "/abs\x00x", "~"} {
		r := e.get("/api/fs?path=" + url.QueryEscape(p))
		if r.Code != 200 || r.obj()["path"] != "" {
			t.Errorf("fs %q: %s", p, r)
		}
	}
	for _, p := range []string{filepath.Join(e.base, "nope"), filepath.Join(e.outside, "secret.txt")} {
		if r := e.get("/api/fs?path=" + url.QueryEscape(p)); r.Code != 400 || r.code() != "bad_path" {
			t.Errorf("fs %q: %s", p, r)
		}
	}
	// no file names or contents are listed
	if r := e.get("/api/fs?path=" + url.QueryEscape(e.outside)); strings.Contains(string(r.Body), "secret.txt") || strings.Contains(string(r.Body), "SECRET") {
		t.Errorf("fs lists files: %s", r)
	}
	if r := e.get("/api/fs?path=" + url.QueryEscape("/")); r.Code != 200 || r.obj()["parent"] != "" {
		t.Errorf("fs /: %s", r)
	}
}

func TestAdv_FolderSwitchWhileRequestsInFlight(t *testing.T) {
	e := newAdvEnv(t)
	a := filepath.Join(e.base, "A")
	b := filepath.Join(e.base, "B")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		e.write(filepath.Join(d, "a.txt"), []byte("- [ ] one\r\n- [ ] two\r\n"))
	}
	var bad atomic.Int32
	stop := make(chan struct{})
	var wg sync.WaitGroup
	work := []func(i int) advResp{
		func(int) advResp { return e.get("/api/state") },
		func(int) advResp { return e.getFile("a.txt") },
		func(i int) advResp {
			return e.putFile("path", "p.txt", "text", fmt.Sprintf("- [ ] one\n- [ ] two\n%d\n", i), "create", true, "overwrite", true, "eol", "crlf")
		},
		func(int) advResp { return e.tick("a.txt", 1, true, nil) },
		func(int) advResp { return e.get("/api/stat?path=a.txt&path=b.txt") },
		func(int) advResp { return e.get("/api/resolve?abs=" + url.QueryEscape(filepath.Join(a, "a.txt"))) },
		func(int) advResp { return e.get("/api/settings") },
		func(int) advResp { return e.get("/api/bootstrap") },
	}
	// (settings PUT is left out on purpose: it shares config.Save's fixed temp file with the folder
	// switch below, which is covered by TestDefect_SettingsConcurrentWritesShareTempFile.)
	for wi, w := range work {
		wg.Add(1)
		go func(wi int, w func(int) advResp) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				if r := w(i); r.Code >= 500 {
					t.Errorf("worker %d: %s", wi, r)
					bad.Add(1)
					return
				}
			}
		}(wi, w)
	}
	wg.Add(1)
	go func() { // background scanner as in Run
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				e.srv.idx.Scan()
				e.srv.broadcast()
			}
		}
	}()
	for i := 0; i < 150; i++ {
		d := a
		if i%2 == 1 {
			d = b
		}
		if r := e.send("POST", "/api/folder", jb("path", d)); r.Code != 200 {
			t.Errorf("switch %d: %s", i, r)
			break
		}
	}
	close(stop)
	wg.Wait()
	noTmpFiles(t, e.base)
	for _, d := range []string{a, b} {
		got := string(e.read(filepath.Join(d, "a.txt")))
		if !strings.HasPrefix(got, "- [") {
			t.Errorf("%s/a.txt damaged: %q", d, got)
		}
	}
}

// ------------------------------------------------------------------ (h) settings

func TestAdv_SettingsSanitisation(t *testing.T) {
	e := newAdvEnv(t)
	long := make([]string, 1000)
	for i := range long {
		long[i] = fmt.Sprintf("f%d.txt", i)
	}
	long[3] = strings.Repeat("x", 501)
	long[4] = "has\x00nul.txt"
	long[5] = ""
	r := e.send("PUT", "/api/settings", jb(
		"folder", "/etc", "theme", "<script>alert(1)</script>", "fontSize", 99999, "wordWrap", true, "panelVisible", true,
		"panelWidth", -5, "groupBy", "'; DROP TABLE", "showDone", true, "openFiles", long))
	if r.Code != 200 {
		t.Fatalf("%s", r)
	}
	var st struct {
		Folder, Theme, GroupBy string
		FontSize, PanelWidth   int
		OpenFiles              []string
	}
	if err := json.Unmarshal(r.Body, &st); err != nil {
		t.Fatal(err)
	}
	if st.Folder == "/etc" || st.Folder != "" {
		t.Errorf("folder field not ignored: %q", st.Folder)
	}
	if st.Theme != "dark" || st.FontSize != 14 || st.PanelWidth != 360 || st.GroupBy != "priority" {
		t.Errorf("not sanitised: %+v", st)
	}
	if len(st.OpenFiles) != 30 {
		t.Errorf("openFiles len %d", len(st.OpenFiles))
	}
	for _, f := range st.OpenFiles {
		if f == "" || len(f) > 500 || strings.ContainsRune(f, 0) {
			t.Errorf("bad openFiles entry %q", f)
		}
	}
	g := e.get("/api/settings").obj()
	if g["folder"] != e.root {
		t.Errorf("GET settings folder=%v, want the live root", g["folder"])
	}
	var disk map[string]any
	if err := json.Unmarshal(e.read(e.cfg), &disk); err != nil {
		t.Fatalf("settings file not valid JSON: %v", err)
	}
	if disk["folder"] == "/etc" {
		t.Error("client folder persisted")
	}
	// boundaries
	for _, c := range []struct{ fs, pw, wantFS, wantPW int }{{7, 219, 14, 360}, {8, 220, 8, 220}, {40, 900, 40, 900}, {41, 901, 14, 360}, {0, 0, 14, 360}} {
		r := e.send("PUT", "/api/settings", jb("fontSize", c.fs, "panelWidth", c.pw))
		o := r.obj()
		if int(o["fontSize"].(float64)) != c.wantFS || int(o["panelWidth"].(float64)) != c.wantPW {
			t.Errorf("%+v -> %v/%v", c, o["fontSize"], o["panelWidth"])
		}
	}
	for _, body := range []string{`{"fontSize":"big"}`, `{"theme":5}`, `{"openFiles":"a"}`, `{"openFiles":[1,2]}`, `{"fontSize":1e999}`, `{"fontSize":1.5}`, `{"nope":1}`, `[]`, `5`} {
		if r := e.send("PUT", "/api/settings", []byte(body)); r.Code != 400 {
			t.Errorf("%s: %s", body, r)
		}
	}
	// the folder can only be changed through /api/folder
	if e.srv.root() != e.root {
		t.Errorf("root changed")
	}
}

func TestDefect_SettingsConcurrentWritesShareTempFile(t *testing.T) {
	e := newAdvEnv(t)
	var wg sync.WaitGroup
	var non200 atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if r := e.send("PUT", "/api/settings", jb("theme", "light", "fontSize", 10+i, "openFiles", []string{strings.Repeat("a", 100+i)})); r.Code != 200 {
					non200.Add(1)
				}
			}
		}(i)
	}
	wg.Wait()
	var m map[string]any
	if err := json.Unmarshal(e.read(e.cfg), &m); err != nil {
		t.Errorf("settings.json corrupt after concurrent saves: %v", err)
	}
	if n := non200.Load(); n > 0 {
		t.Errorf("DEFECT: %d concurrent settings saves failed (shared %q temp file)", n, "settings.json.trimple-tmp")
	}
	noTmpFiles(t, e.base)
}

func TestAdv_CorruptConfigFilesFallBackToDefaults(t *testing.T) {
	big := bytes.Repeat([]byte("{\"theme\":\"dark\"},"), 600000) // ~10 MB of junk
	cases := map[string][]byte{
		"empty": {}, "brace": []byte("{"), "null": []byte("null"), "array": []byte("[]"), "binary": {0, 1, 2, 0xff, 0xfe, 0x80},
		"wrong types": []byte(`{"fontSize":"big","theme":7,"openFiles":"x"}`), "utf16": append([]byte{0xFF, 0xFE}, []byte("{\x00}\x00")...),
		"extreme":    []byte(`{"theme":"x","fontSize":1e9,"panelWidth":-1,"groupBy":[],"folder":"/nonexistent/zzz","openFiles":[null,1,"a"]}`),
		"nul folder": []byte(`{"folder":"a\u0000b"}`), "huge folder": []byte(`{"folder":"` + strings.Repeat("a", 5000) + `"}`),
		"folder is file": nil, "folder relative": []byte(`{"folder":"."}`), "junk": big, "trailing": []byte(`{"theme":"light"} garbage`),
		"deep": []byte(`{"openFiles":` + strings.Repeat("[", 100000) + `}`),
	}
	for name, content := range cases {
		name, content := name, content
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			cfg := filepath.Join(base, "settings.json")
			if name == "folder is file" {
				f := filepath.Join(base, "a-file.txt")
				_ = os.WriteFile(f, []byte("x"), 0o644)
				content, _ = json.Marshal(map[string]any{"folder": f})
			}
			if name == "folder relative" {
				// keep the process cwd out of it
				if _, err := os.Getwd(); err != nil {
					t.Skip()
				}
			}
			_ = os.WriteFile(cfg, content, 0o644)
			s, err := New(Options{ConfigPath: cfg, Token: advToken, KeepAlive: true})
			if err != nil {
				t.Fatalf("New failed on corrupt config: %v", err)
			}
			st := s.settings
			if (st.Theme != "dark" && st.Theme != "light") || st.FontSize < 8 || st.FontSize > 40 || st.PanelWidth < 220 || st.PanelWidth > 900 || len(st.OpenFiles) > 30 {
				t.Errorf("insane settings survived: %+v", st)
			}
			if name == "folder is file" || name == "extreme" || name == "nul folder" || name == "huge folder" {
				if s.root() != "" {
					t.Errorf("root=%q from bogus folder", s.root())
				}
			}
			ts := httptest.NewRecorder()
			req := localCtx(httptest.NewRequest("GET", "/api/bootstrap", nil), advPort)
			req.Host = advHost
			req.Header.Set("X-Trimple-Token", advToken)
			s.Handler().ServeHTTP(ts, req)
			if ts.Code != 200 {
				t.Errorf("bootstrap: %d", ts.Code)
			}
			// saving over a corrupt file works and yields valid JSON
			e := &advEnv{t: t, srv: s, h: s.Handler()}
			if r := e.send("PUT", "/api/settings", jb("theme", "light")); r.Code != 200 {
				t.Errorf("save over corrupt config: %s", r)
			}
			var m map[string]any
			if err := json.Unmarshal(e.read(cfg), &m); err != nil {
				t.Errorf("settings.json invalid after save: %v", err)
			}
		})
	}
}

func TestAdv_SavedFolderIsUsedAndRelativeRootIsAbsolutised(t *testing.T) {
	base := t.TempDir()
	notesDir := filepath.Join(base, "n")
	_ = os.Mkdir(notesDir, 0o755)
	cfg := filepath.Join(base, "s.json")
	b, _ := json.Marshal(map[string]any{"folder": notesDir})
	_ = os.WriteFile(cfg, b, 0o644)
	s, err := New(Options{ConfigPath: cfg, Token: advToken})
	if err != nil || s.root() != notesDir {
		t.Fatalf("saved folder: %v %q", err, s.root())
	}
	// --folder wins over the saved one and relative values become absolute
	wd, _ := os.Getwd()
	rel, err := filepath.Rel(wd, notesDir)
	if err != nil {
		t.Skip()
	}
	s, err = New(Options{ConfigPath: cfg, Token: advToken, Root: rel})
	if err != nil || !filepath.IsAbs(s.root()) {
		t.Errorf("relative --folder: %v %q", err, s.root())
	}
	if _, err := New(Options{ConfigPath: cfg}); err == nil {
		t.Error("empty token accepted")
	}
}

// ------------------------------------------------------------------ quit

func TestAdv_QuitNeedsTokenAndPOST(t *testing.T) {
	e := newAdvEnv(t)
	for _, r := range []advResp{
		e.send("POST", "/api/quit", nil, noToken), e.send("GET", "/api/quit", nil), e.send("POST", "/api/quit", nil, hdr("Origin", "https://evil.com")),
		e.send("POST", "/api/quit", nil, host("evil.com")), e.send("POST", "/api/quit?t="+advToken, nil, noToken),
	} {
		if r.Code == 200 {
			t.Errorf("quit accepted: %s", r)
		}
	}
	select {
	case <-e.srv.quit:
		t.Fatal("server quit")
	default:
	}
	if r := e.send("POST", "/api/quit", nil); r.Code != 200 {
		t.Errorf("quit: %s", r)
	}
	select {
	case <-e.srv.quit:
	case <-time.After(2 * time.Second):
		t.Error("valid quit did not stop the server")
	}
}

// ------------------------------------------------------------------ (i) SSE, lifetime (real sockets)

type runEnv struct {
	*Server
	base   string
	cancel context.CancelFunc
	done   chan error
	root   string
}

func startRun(t *testing.T, mod func(*Options)) *runEnv {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "notes")
	_ = os.MkdirAll(root, 0o755)
	o := Options{Root: root, ConfigPath: filepath.Join(dir, "cfg", "s.json"), Token: advToken, ScanEvery: 40 * time.Millisecond, KeepAlive: true}
	if mod != nil {
		mod(&o)
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Listen(0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	re := &runEnv{Server: s, base: fmt.Sprintf("http://127.0.0.1:%d", s.Port()), cancel: cancel, done: make(chan error, 1), root: root}
	go func() { re.done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-re.done:
		case <-time.After(6 * time.Second):
			t.Error("Run did not return on cleanup")
		}
	})
	return re
}

func (re *runEnv) exited(within time.Duration) bool {
	select {
	case <-re.done:
		re.done <- nil
		return true
	case <-time.After(within):
		return false
	}
}

type sseConn struct {
	cancel context.CancelFunc
	resp   *http.Response
	lines  chan string
}

func (re *runEnv) sse(t *testing.T, target string, mods ...func(*http.Request)) *sseConn {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", re.base+target, nil)
	for _, m := range mods {
		m(req)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	c := &sseConn{cancel: cancel, resp: resp, lines: make(chan string, 256)}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case c.lines <- sc.Text():
			default:
			}
		}
		close(c.lines)
	}()
	t.Cleanup(cancel)
	return c
}

func (c *sseConn) next(d time.Duration) (string, bool) {
	select {
	case l, ok := <-c.lines:
		return l, ok
	case <-time.After(d):
		return "", false
	}
}

// waitVersion reads events until a version > min is seen, returns it.
func (c *sseConn) waitVersion(min int, d time.Duration) (int, bool) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		l, ok := c.next(time.Until(deadline))
		if !ok {
			return 0, false
		}
		if strings.HasPrefix(l, "data: ") {
			if v, err := strconv.Atoi(strings.TrimPrefix(l, "data: ")); err == nil && v > min {
				return v, true
			}
		}
	}
	return 0, false
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestAdv_SSEAuthAndHost(t *testing.T) {
	re := startRun(t, nil)
	get := func(target string, mods ...func(*http.Request)) int {
		req, _ := http.NewRequest("GET", re.base+target, nil)
		for _, m := range mods {
			m(req)
		}
		resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Errorf("%s unexpectedly opened a stream", target)
		}
		return resp.StatusCode
	}
	if c := get("/api/events"); c != 401 {
		t.Errorf("no token: %d", c)
	}
	if c := get("/api/events?t=wrong"); c != 401 {
		t.Errorf("wrong token: %d", c)
	}
	if c := get("/api/events?t=" + advToken[:10]); c != 401 {
		t.Errorf("prefix token: %d", c)
	}
	if c := get("/api/events?t="+advToken, func(r *http.Request) { r.Host = "evil.com:" + strconv.Itoa(re.Port()) }); c != 403 {
		t.Errorf("rebinding host: %d", c)
	}
	if c := get("/api/events?t="+advToken, func(r *http.Request) { r.Host = "127.0.0.1" }); c != 403 {
		t.Errorf("host without port: %d", c)
	}
	if n := re.clientCount(); n != 0 {
		t.Errorf("rejected requests counted as windows: %d", n)
	}
}

func TestAdv_SSEInitialEventChangeEventAndDisconnect(t *testing.T) {
	re := startRun(t, nil)
	c := re.sse(t, "/api/events?t="+advToken)
	if ct := c.resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type %q", ct)
	}
	if c.resp.Header.Get("Cache-Control") != "no-store" || c.resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers: %v", c.resp.Header)
	}
	if l, ok := c.next(2 * time.Second); !ok || l != "event: version" {
		t.Fatalf("first line %q ok=%v", l, ok)
	}
	l, _ := c.next(2 * time.Second)
	if !strings.HasPrefix(l, "data: ") {
		t.Fatalf("second line %q", l)
	}
	if !waitFor(2*time.Second, func() bool { return re.clientCount() == 1 }) {
		t.Fatalf("clientCount=%d", re.clientCount())
	}
	// let the initial background scan settle, then change a file
	time.Sleep(300 * time.Millisecond)
	last := 0
	for {
		l, ok := c.next(150 * time.Millisecond)
		if !ok {
			break
		}
		if strings.HasPrefix(l, "data: ") {
			last, _ = strconv.Atoi(strings.TrimPrefix(l, "data: "))
		}
	}
	if err := os.WriteFile(filepath.Join(re.root, "new.txt"), []byte("- [ ] hello\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, ok := c.waitVersion(last, 3*time.Second)
	if !ok {
		t.Fatalf("no event after a file appeared (last version %d)", last)
	}
	// delete -> another event
	if err := os.Remove(filepath.Join(re.root, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.waitVersion(v, 3*time.Second); !ok {
		t.Error("no event after a file was deleted")
	}
	// a header token works as well
	c2 := re.sse(t, "/api/events", hdr("X-Trimple-Token", advToken))
	if l, ok := c2.next(2 * time.Second); !ok || l != "event: version" {
		t.Errorf("header-token stream: %q", l)
	}
	if !waitFor(2*time.Second, func() bool { return re.clientCount() == 2 }) {
		t.Errorf("clientCount=%d want 2", re.clientCount())
	}
	c.cancel()
	if !waitFor(3*time.Second, func() bool { return re.clientCount() == 1 }) {
		t.Errorf("after one disconnect clientCount=%d want 1", re.clientCount())
	}
	c2.cancel()
	if !waitFor(3*time.Second, func() bool { return re.clientCount() == 0 }) {
		t.Errorf("after all disconnect clientCount=%d want 0", re.clientCount())
	}
	re.subMu.Lock()
	n := len(re.subs)
	re.subMu.Unlock()
	if n != 0 {
		t.Errorf("%d subscriber channels leaked", n)
	}
}

func TestAdv_SSEManyConnectDisconnectNoLeak(t *testing.T) {
	re := startRun(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			req, _ := http.NewRequestWithContext(ctx, "GET", re.base+"/api/events?t="+advToken, nil)
			resp, err := (&http.Client{}).Do(req)
			if err == nil {
				buf := make([]byte, 64)
				_, _ = resp.Body.Read(buf)
			}
			time.Sleep(time.Duration(i%5) * 10 * time.Millisecond)
			cancel()
		}()
	}
	wg.Wait()
	if !waitFor(5*time.Second, func() bool { return re.clientCount() == 0 }) {
		t.Errorf("clientCount=%d after all clients left", re.clientCount())
	}
	re.cliMu.Lock()
	neg := re.clients < 0
	re.cliMu.Unlock()
	if neg {
		t.Error("client counter went negative")
	}
}

func TestAdv_SSEKeepalive(t *testing.T) {
	if testing.Short() {
		t.Skip("takes ~16s")
	}
	re := startRun(t, nil)
	c := re.sse(t, "/api/events?t="+advToken)
	deadline := time.Now().Add(18 * time.Second)
	for time.Now().Before(deadline) {
		l, ok := c.next(time.Until(deadline))
		if !ok {
			break
		}
		if l == ": ping" {
			return
		}
	}
	t.Error("no keep-alive comment within 18s")
}

func TestAdv_AutoExitNeverConnected(t *testing.T) {
	re := startRun(t, func(o *Options) {
		o.KeepAlive = false
		o.StartupWait = 300 * time.Millisecond
		o.Grace = 300 * time.Millisecond
	})
	if !re.exited(4 * time.Second) {
		t.Fatal("Run did not return although no window ever connected")
	}
}

func TestAdv_KeepAliveNeverExits(t *testing.T) {
	re := startRun(t, func(o *Options) {
		o.KeepAlive = true
		o.StartupWait = 200 * time.Millisecond
		o.Grace = 200 * time.Millisecond
	})
	c := re.sse(t, "/api/events?t="+advToken)
	c.next(time.Second)
	c.cancel()
	if re.exited(2500 * time.Millisecond) {
		t.Fatal("KeepAlive server exited")
	}
}

func TestAdv_AutoExitAfterLastWindowLeaves(t *testing.T) {
	re := startRun(t, func(o *Options) {
		o.KeepAlive = false
		o.StartupWait = 300 * time.Millisecond
		o.Grace = 400 * time.Millisecond
	})
	c := re.sse(t, "/api/events?t="+advToken)
	c.next(time.Second)
	if re.exited(1800 * time.Millisecond) { // past StartupWait + Grace, but a window is open
		t.Fatal("exited while a window was connected")
	}
	c.cancel()
	t0 := time.Now()
	if !re.exited(4 * time.Second) {
		t.Fatal("did not exit after the last window left")
	}
	if time.Since(t0) < 300*time.Millisecond {
		t.Errorf("exited %v after disconnect, before Grace elapsed", time.Since(t0))
	}
}

func TestAdv_ReconnectWithinGraceKeepsServerAlive(t *testing.T) {
	re := startRun(t, func(o *Options) {
		o.KeepAlive = false
		o.StartupWait = 300 * time.Millisecond
		o.Grace = 1200 * time.Millisecond
	})
	c := re.sse(t, "/api/events?t="+advToken)
	c.next(time.Second)
	c.cancel() // page reload
	time.Sleep(300 * time.Millisecond)
	c2 := re.sse(t, "/api/events?t="+advToken)
	c2.next(time.Second)
	if re.exited(2500 * time.Millisecond) {
		t.Fatal("exited although a window reconnected inside the grace period")
	}
	c2.cancel()
	if !re.exited(5 * time.Second) {
		t.Fatal("did not exit after final disconnect")
	}
}

func TestAdv_QuitEndpointStopsRunAndForeignOriginDoesNot(t *testing.T) {
	re := startRun(t, nil)
	post := func(mods ...func(*http.Request)) int {
		req, _ := http.NewRequest("POST", re.base+"/api/quit", nil)
		req.Header.Set("X-Trimple-Token", advToken)
		for _, m := range mods {
			m(req)
		}
		resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := post(hdr("Origin", "https://evil.com")); c != 403 {
		t.Errorf("foreign origin quit: %d", c)
	}
	if re.exited(400 * time.Millisecond) {
		t.Fatal("foreign-origin quit stopped the server")
	}
	if c := post(); c != 200 {
		t.Fatalf("quit: %d", c)
	}
	if !re.exited(3 * time.Second) {
		t.Error("quit did not stop Run")
	}
}

// Ctrl+C (ctx cancel) with a window open: SSE handlers only watch s.quit and the request context,
// not the shutdown context, so Shutdown waits for its full 2s timeout before giving up.
func TestAdv_ShutdownWithOpenStreamIsBounded(t *testing.T) {
	re := startRun(t, nil)
	c := re.sse(t, "/api/events?t="+advToken)
	c.next(time.Second)
	t0 := time.Now()
	re.cancel()
	if !re.exited(6 * time.Second) {
		t.Fatal("Run did not return after ctx cancel with an open stream")
	}
	el := time.Since(t0)
	t.Logf("Run returned %v after cancel with an open SSE stream", el)
	if el > 3*time.Second {
		t.Errorf("shutdown too slow: %v", el)
	}
}

// Real-socket sanity: a handful of the guard behaviours through net/http with its own parsing.
func TestAdv_RealSocketGuards(t *testing.T) {
	re := startRun(t, nil)
	do := func(method, target string, body string, mods ...func(*http.Request)) (int, string) {
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, _ := http.NewRequest(method, re.base+target, rd)
		req.Header.Set("X-Trimple-Token", advToken)
		for _, m := range mods {
			m(req)
		}
		resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, target, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if c, _ := do("GET", "/api/ping", ""); c != 200 {
		t.Errorf("ping: %d", c)
	}
	if c, _ := do("GET", "/api/ping", "", func(r *http.Request) { r.Host = "localhost:" + strconv.Itoa(re.Port()) }); c != 200 {
		t.Errorf("localhost host: %d", c)
	}
	if c, _ := do("GET", "/api/ping", "", func(r *http.Request) { r.Host = "evil.com:" + strconv.Itoa(re.Port()) }); c != 403 {
		t.Errorf("evil host: %d", c)
	}
	if c, _ := do("GET", "/api/ping", "", noToken); c != 401 {
		t.Errorf("no token: %d", c)
	}
	if c, _ := do("PUT", "/api/file", `{"path":"real.txt","text":"hi","create":true}`, hdr("Origin", "null")); c != 403 {
		t.Errorf("null origin: %d", c)
	}
	if c, b := do("PUT", "/api/file", `{"path":"real.txt","text":"hi","create":true}`, hdr("Origin", re.base)); c != 200 {
		t.Errorf("same-origin PUT: %d %s", c, b)
	}
	if b, err := os.ReadFile(filepath.Join(re.root, "real.txt")); err != nil || string(b) != "hi" {
		t.Errorf("file: %q %v", b, err)
	}
	// oversize over the wire
	big := `{"path":"real.txt","text":"` + strings.Repeat("a", 25<<20) + `"}`
	req, _ := http.NewRequest("PUT", re.base+"/api/file", strings.NewReader(big))
	req.Header.Set("X-Trimple-Token", advToken)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Error("25 MB body accepted")
		}
	}
	if b, _ := os.ReadFile(filepath.Join(re.root, "real.txt")); string(b) != "hi" {
		t.Errorf("oversize PUT changed the file: %d bytes", len(b))
	}
	// slow-loris style: header timeout closes idle half-open connections
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", re.Port()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = io.WriteString(c, "GET /api/ping HTTP/1.1\r\nHost: 127.0.0.1\r\n")
	_ = c.SetReadDeadline(time.Now().Add(8 * time.Second))
	buf := make([]byte, 1)
	if _, err := c.Read(buf); err == nil {
		t.Log("server answered an incomplete request")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Errorf("incomplete request header not timed out within 8s (ReadHeaderTimeout is 5s)")
	}
}

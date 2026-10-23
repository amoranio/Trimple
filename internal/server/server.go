// Package server is Trimple's local HTTP backend and the host for the embedded web UI.
//
// It listens on 127.0.0.1 only. Every API call needs a per-run secret token, requests whose Host
// header is not the loopback address are refused (DNS rebinding), and state-changing requests from
// another origin are refused (CSRF). File paths from the client are always relative to the chosen
// notes folder and are validated against escaping it.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/amoranio/trimple/internal/config"
	"github.com/amoranio/trimple/internal/notes"
	"github.com/amoranio/trimple/web"
)

// Version is set by the linker for release builds.
var Version = "dev"

// OpenInNotepad starts an external editor for a file. Overridable in tests.
var OpenInNotepad = openInNotepad

type Options struct {
	Root        string        // notes folder; empty = use the saved one
	ConfigPath  string        // settings file
	Token       string        // per-run secret
	KeepAlive   bool          // never exit when the window closes
	Grace       time.Duration // how long to wait after the last window closes
	StartupWait time.Duration // how long to wait for the first window
	ScanEvery   time.Duration
	Logf        func(format string, args ...any)
}

type Server struct {
	opts Options
	idx  *notes.Index

	mu       sync.RWMutex
	settings config.Settings

	ln      net.Listener
	httpSrv *http.Server

	subMu sync.Mutex
	subs  map[chan struct{}]struct{}

	cliMu         sync.Mutex
	clients       int
	everConnected bool
	lastLeave     time.Time
	started       time.Time

	quit     chan struct{}
	quitOnce sync.Once
}

// New loads settings and prepares the index. It does not listen yet.
func New(o Options) (*Server, error) {
	if o.Token == "" {
		return nil, errors.New("server: empty token")
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Grace <= 0 {
		o.Grace = 20 * time.Second
	}
	if o.StartupWait <= 0 {
		o.StartupWait = 2 * time.Minute
	}
	if o.ScanEvery <= 0 {
		o.ScanEvery = 1500 * time.Millisecond
	}
	if o.ConfigPath == "" {
		o.ConfigPath = config.DefaultPath()
	}
	s := &Server{
		opts: o, settings: config.Load(o.ConfigPath),
		subs: map[chan struct{}]struct{}{}, quit: make(chan struct{}), started: time.Now(),
	}
	root := o.Root
	if root == "" && notes.Exists(s.settings.Folder) {
		root = s.settings.Folder
	}
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		if !notes.Exists(root) {
			root = ""
		}
	}
	s.idx = notes.NewIndex(root)
	s.idx.Scan()
	return s, nil
}

// Listen binds 127.0.0.1, preferring the given port and falling back to any free one.
func (s *Server) Listen(preferred int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred))
	if err != nil && preferred != 0 {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return err
	}
	s.ln = ln
	return nil
}

func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// URL is the address of the UI, including the token the page needs to call the API.
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", s.Port(), s.opts.Token)
}

// Quit asks Run to return.
func (s *Server) Quit() { s.quitOnce.Do(func() { close(s.quit) }) }

// Handler exposes the guarded mux (used by tests).
func (s *Server) Handler() http.Handler { return s.routes() }

// Run serves until ctx is cancelled or Quit is called (or the window has been closed).
func (s *Server) Run(ctx context.Context) error {
	s.httpSrv = &http.Server{Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- s.httpSrv.Serve(s.ln) }()
	go s.scanLoop(ctx)
	go s.watchWindows(ctx)

	var err error
	select {
	case <-ctx.Done():
	case <-s.quit:
	case err = <-errc:
	}
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.httpSrv.Shutdown(sctx)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func (s *Server) scanLoop(ctx context.Context) {
	t := time.NewTicker(s.opts.ScanEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.quit:
			return
		case <-t.C:
			if s.clientCount() > 0 && s.idx.Scan() {
				s.broadcast()
			}
		}
	}
}

// watchWindows quits the app once its window has been closed for a while.
func (s *Server) watchWindows(ctx context.Context) {
	if s.opts.KeepAlive {
		return
	}
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.quit:
			return
		case <-t.C:
			s.cliMu.Lock()
			idle := s.clients == 0 && ((s.everConnected && time.Since(s.lastLeave) > s.opts.Grace) ||
				(!s.everConnected && time.Since(s.started) > s.opts.StartupWait))
			s.cliMu.Unlock()
			if idle {
				s.opts.Logf("no window connected; exiting")
				s.Quit()
				return
			}
		}
	}
}

func (s *Server) clientCount() int {
	s.cliMu.Lock()
	defer s.cliMu.Unlock()
	return s.clients
}

func (s *Server) clientJoin() {
	s.cliMu.Lock()
	s.clients++
	s.everConnected = true
	s.cliMu.Unlock()
}

func (s *Server) clientLeave() {
	s.cliMu.Lock()
	s.clients--
	s.lastLeave = time.Now()
	s.cliMu.Unlock()
}

func (s *Server) broadcast() {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Server) scanNow() {
	s.idx.Scan()
	s.broadcast()
}

func (s *Server) root() string { return s.idx.Root() }

// ---------------------------------------------------------------- routing and guards

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", s.handlePing)
	mux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/file", s.handleFile)
	mux.HandleFunc("/api/stat", s.handleStat)
	mux.HandleFunc("/api/task", s.handleTask)
	mux.HandleFunc("/api/notepad", s.handleNotepad)
	mux.HandleFunc("/api/fs", s.handleFS)
	mux.HandleFunc("/api/folder", s.handleFolder)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/resolve", s.handleResolve)
	mux.HandleFunc("/api/quit", s.handleQuit)
	mux.Handle("/", http.FileServer(http.FS(web.Files)))
	return s.guard(mux)
}

const csp = "default-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func localPort(r *http.Request) string {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if _, p, err := net.SplitHostPort(a.String()); err == nil {
			return p
		}
	}
	return ""
}

// loopbackHost accepts only 127.0.0.1 / localhost on the port we are actually serving.
func loopbackHost(hostport, port string) bool {
	h, p, err := net.SplitHostPort(hostport)
	if err != nil || port == "" || p != port {
		return false
	}
	return h == "127.0.0.1" || strings.EqualFold(h, "localhost")
}

func (s *Server) tokenOK(r *http.Request) bool {
	got := r.Header.Get("X-Trimple-Token")
	if got == "" && r.URL.Path == "/api/events" { // EventSource cannot set headers
		got = r.URL.Query().Get("t")
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.opts.Token)) == 1
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		port := localPort(r)
		if !loopbackHost(r.Host, port) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", csp)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !s.tokenOK(r) {
				fail(w, http.StatusUnauthorized, "auth", "missing or wrong token; reopen Trimple")
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				if o := r.Header.Get("Origin"); o != "" {
					u, err := url.Parse(o)
					if err != nil || !loopbackHost(u.Host, port) {
						fail(w, http.StatusForbidden, "origin", "cross-origin request refused")
						return
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- helpers

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: msg, Code: code})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 24<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid request: "+err.Error())
		return false
	}
	if dec.More() {
		fail(w, http.StatusBadRequest, "bad_request", "invalid request: trailing data")
		return false
	}
	return true
}

func method(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for _, m := range allowed {
		if r.Method == m {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	fail(w, http.StatusMethodNotAllowed, "method", "method not allowed")
	return false
}

func (s *Server) resolve(w http.ResponseWriter, rel string) (string, bool) {
	abs, err := safeJoin(s.root(), rel)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_path", err.Error())
		return "", false
	}
	return abs, true
}

var eolNames = map[string]string{"\r\n": "crlf", "\n": "lf", "\r": "cr"}
var eolValues = map[string]string{"crlf": "\r\n", "lf": "\n", "cr": "\r"}

// ---------------------------------------------------------------- handlers

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"app": "trimple", "version": Version})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	st := s.settings
	s.mu.RUnlock()
	st.Folder = s.root()
	writeJSON(w, http.StatusOK, map[string]any{
		"app": "trimple", "version": Version, "os": runtime.GOOS,
		"notepad": runtime.GOOS == "windows", "folder": s.root(), "settings": st,
	})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	q := r.URL.Query()
	snap := s.idx.Snapshot()
	sel := notes.Select(snap.Tasks, notes.Query{Label: q.Get("label"), Search: q.Get("q"), ShowDone: q.Get("done") == "1"})
	writeJSON(w, http.StatusOK, map[string]any{
		"version": snap.Version, "folder": snap.Root, "truncated": snap.Truncated,
		"counts": notes.CountOpen(snap.Tasks), "totalTasks": len(snap.Tasks),
		"labels": notes.Labels(snap.Tasks), "groups": notes.GroupTasks(sel, q.Get("group")),
		"files": snap.Files, "errors": snap.Errors,
	})
}

func displayText(f *notes.File) string {
	t := notes.NormalizeNewlines(f.Text)
	if !f.Lossless { // shown read-only; make it safe to transport
		t = strings.ReplaceAll(t, "\x00", "?")
		t = strings.ToValidUTF8(t, "�")
	}
	return t
}

type fileResp struct {
	Path     string `json:"path"`
	Text     string `json:"text"`
	Kind     string `json:"kind"`
	EOL      string `json:"eol"`
	Lossless bool   `json:"lossless"`
	Problem  string `json:"problem"`
	Hash     string `json:"hash"`
	Stamp    string `json:"stamp"`
}

type putFile struct {
	Path         string `json:"path"`
	Text         string `json:"text"`
	Kind         string `json:"kind"`
	EOL          string `json:"eol"`
	ExpectedHash string `json:"expectedHash"`
	Create       bool   `json:"create"`
	Overwrite    bool   `json:"overwrite"`
	AllowUpgrade bool   `json:"allowUpgrade"`
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rel := r.URL.Query().Get("path")
		abs, ok := s.resolve(w, rel)
		if !ok {
			return
		}
		f, err := notes.ReadFile(abs)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				fail(w, http.StatusNotFound, "missing", "that file no longer exists")
				return
			}
			fail(w, http.StatusInternalServerError, "read", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, fileResp{
			Path: rel, Text: displayText(f), Kind: string(f.Kind), EOL: eolNames[f.EOL],
			Lossless: f.Lossless, Problem: f.Problem, Hash: f.Hash, Stamp: f.Stamp,
		})
	case http.MethodPut:
		s.putFile(w, r)
	default:
		method(w, r, http.MethodGet, http.MethodPut)
	}
}

func (s *Server) putFile(w http.ResponseWriter, r *http.Request) {
	var req putFile
	if !decode(w, r, &req) {
		return
	}
	abs, ok := s.resolve(w, req.Path)
	if !ok {
		return
	}
	if !strings.EqualFold(filepath.Ext(abs), ".txt") {
		fail(w, http.StatusBadRequest, "bad_path", "only .txt notes can be saved")
		return
	}
	if strings.ContainsRune(req.Text, 0) {
		fail(w, http.StatusBadRequest, "bad_text", "NUL characters cannot be saved")
		return
	}
	eol, ok := eolValues[req.EOL]
	if !ok {
		eol = "\r\n"
	}
	kind := notes.Kind(req.Kind)
	switch kind {
	case notes.UTF8, notes.UTF8BOM, notes.UTF16LE, notes.UTF16BE, notes.ANSI:
	default:
		kind = notes.UTF8
	}

	var current *notes.File
	if _, err := os.Stat(abs); err == nil {
		cur, err := notes.ReadFile(abs)
		if err != nil {
			fail(w, http.StatusInternalServerError, "read", err.Error())
			return
		}
		current = cur
	}
	switch {
	case req.Create && current != nil && !req.Overwrite:
		fail(w, http.StatusConflict, "exists", "a note with that name already exists")
		return
	case current != nil && !current.Lossless:
		fail(w, http.StatusConflict, "readonly", "that file "+current.Problem+", so Trimple will not rewrite it")
		return
	case !req.Create && current != nil && req.ExpectedHash != "" && current.Hash != req.ExpectedHash && !req.Overwrite:
		fail(w, http.StatusConflict, "changed", "the file was changed outside Trimple")
		return
	}

	data, usedKind := notes.Encode(notes.ApplyEOL(req.Text, eol), kind)
	if usedKind != kind && !req.AllowUpgrade {
		fail(w, http.StatusConflict, "encoding", "the text contains characters the file's old ANSI encoding cannot store")
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		fail(w, http.StatusInternalServerError, "write", err.Error())
		return
	}
	if err := notes.WriteFileAtomic(abs, data); err != nil {
		fail(w, http.StatusInternalServerError, "write", describeWriteError(err))
		return
	}
	s.idx.Forget(req.Path)
	s.scanNow()
	writeJSON(w, http.StatusOK, map[string]string{"hash": notes.Hash(data), "stamp": notes.StampOf(abs), "kind": string(usedKind)})
}

func describeWriteError(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "Windows refused the write (the file may be read-only or open elsewhere): " + err.Error()
	}
	return err.Error()
}

func (s *Server) handleStat(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	paths := r.URL.Query()["path"]
	if len(paths) > 60 {
		paths = paths[:60]
	}
	type st struct {
		Exists bool   `json:"exists"`
		Stamp  string `json:"stamp"`
	}
	out := map[string]st{}
	for _, rel := range paths {
		abs, err := safeJoin(s.root(), rel)
		if err != nil {
			out[rel] = st{}
			continue
		}
		stamp := notes.StampOf(abs)
		out[rel] = st{Exists: stamp != "", Stamp: stamp}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Path string  `json:"path"`
		Line int     `json:"line"`
		Done bool    `json:"done"`
		Raw  *string `json:"raw"`
	}
	if !decode(w, r, &req) {
		return
	}
	abs, ok := s.resolve(w, req.Path)
	if !ok {
		return
	}
	err := notes.SetTaskDone(abs, req.Line, req.Done, req.Raw)
	s.idx.Forget(req.Path)
	s.scanNow()
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, notes.ErrStale), errors.Is(err, notes.ErrNoTask):
		fail(w, http.StatusConflict, "stale", err.Error())
	case errors.Is(err, notes.ErrReadOnly):
		fail(w, http.StatusConflict, "readonly", err.Error())
	case errors.Is(err, fs.ErrNotExist):
		fail(w, http.StatusNotFound, "missing", "that file no longer exists")
	default:
		fail(w, http.StatusInternalServerError, "write", describeWriteError(err))
	}
}

func (s *Server) handleNotepad(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &req) {
		return
	}
	abs, ok := s.resolve(w, req.Path)
	if !ok {
		return
	}
	if _, err := os.Stat(abs); err != nil {
		fail(w, http.StatusNotFound, "missing", "that file does not exist yet; save it first")
		return
	}
	if err := OpenInNotepad(abs); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, errNotepadUnsupported) {
			code = http.StatusNotImplemented
		}
		fail(w, code, "notepad", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFS(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	home, _ := os.UserHomeDir()
	suggested := ""
	if home != "" {
		suggested = filepath.Join(home, "Documents", "Trimple Notes")
	}
	p := r.URL.Query().Get("path")
	resp := map[string]any{"home": home, "suggested": suggested, "drives": listDrives()}
	if p == "" {
		p = home
	}
	if p == "" || !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
		resp["path"], resp["dirs"], resp["parent"] = "", []string{}, ""
		writeJSON(w, http.StatusOK, resp)
		return
	}
	p = filepath.Clean(p)
	entries, err := os.ReadDir(p)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_path", "cannot open that folder: "+err.Error())
		return
	}
	dirs := []string{}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
			if len(dirs) >= 1000 {
				break
			}
		}
	}
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	resp["path"], resp["dirs"], resp["parent"] = p, dirs, parent
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleFolder(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Path   string `json:"path"`
		Create bool   `json:"create"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Path == "" || !filepath.IsAbs(req.Path) || strings.ContainsRune(req.Path, 0) {
		fail(w, http.StatusBadRequest, "bad_path", "choose a full folder path")
		return
	}
	p := filepath.Clean(req.Path)
	created := false
	if !notes.Exists(p) {
		if !req.Create {
			fail(w, http.StatusBadRequest, "bad_path", "that folder does not exist")
			return
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			fail(w, http.StatusBadRequest, "bad_path", err.Error())
			return
		}
		created = true
	}
	if created {
		_ = os.WriteFile(filepath.Join(p, "Welcome.txt"), []byte(welcomeText), 0o644)
	}
	s.mu.Lock()
	s.settings.Folder = p
	st := s.settings
	s.mu.Unlock()
	if err := config.Save(s.opts.ConfigPath, st); err != nil {
		s.opts.Logf("saving settings: %v", err)
	}
	s.idx.SetRoot(p)
	s.scanNow()
	writeJSON(w, http.StatusOK, map[string]string{"folder": p})
}

const welcomeText = "# Welcome to Trimple\r\n\r\nThis is a plain text file. Notepad can open it too.\r\nAny line that looks like a checkbox becomes a task in the panel on the left:\r\n\r\n- [ ] Write the weekly report !1 #work\r\n- [ ] Book dentist !3 #home\r\n- [x] Install Trimple #admin\r\n\r\n  [ ] / [x]   open / done        click the box in the panel to tick it\r\n  !1 !2 !3    priority (1 = high)\r\n  #word       label, as many as you like\r\n"

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		st := s.settings
		s.mu.RUnlock()
		st.Folder = s.root()
		writeJSON(w, http.StatusOK, st)
	case http.MethodPut:
		var st config.Settings
		if !decode(w, r, &st) {
			return
		}
		s.mu.Lock()
		st.Folder = s.settings.Folder // the folder is only changed through /api/folder
		st.Sanitize()
		s.settings = st
		s.mu.Unlock()
		if err := config.Save(s.opts.ConfigPath, st); err != nil {
			fail(w, http.StatusInternalServerError, "write", "could not save settings: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	default:
		method(w, r, http.MethodGet, http.MethodPut)
	}
}

// handleResolve maps an absolute path (from the command line) to a path inside the notes folder.
func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	abs := r.URL.Query().Get("abs")
	if abs == "" || !filepath.IsAbs(abs) || strings.ContainsRune(abs, 0) || s.root() == "" {
		writeJSON(w, http.StatusOK, map[string]any{"inside": false})
		return
	}
	rel, ok := relOf(s.root(), filepath.Clean(abs))
	if !ok || !strings.EqualFold(filepath.Ext(rel), ".txt") {
		writeJSON(w, http.StatusOK, map[string]any{"inside": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inside": true, "path": rel})
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go func() { time.Sleep(100 * time.Millisecond); s.Quit() }()
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "sse", "streaming not supported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	s.clientJoin()
	defer s.clientLeave()
	ch := make(chan struct{}, 1)
	s.subMu.Lock()
	s.subs[ch] = struct{}{}
	s.subMu.Unlock()
	defer func() {
		s.subMu.Lock()
		delete(s.subs, ch)
		s.subMu.Unlock()
	}()

	send := func() bool {
		_, err := io.WriteString(w, fmt.Sprintf("event: version\ndata: %d\n\n", s.idx.Snapshot().Version))
		fl.Flush()
		return err == nil
	}
	go func() { // the index may be stale if nobody was watching
		if s.idx.Scan() {
			s.broadcast()
		}
	}()
	if !send() {
		return
	}
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.quit:
			return
		case <-ch:
			if !send() {
				return
			}
		case <-keep.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

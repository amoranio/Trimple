// Command trimple is a Notepad-style notes app with a task panel. It is one self-contained
// executable: it serves the UI from memory on 127.0.0.1 and opens it in an app-style browser window.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/amoranio/trimple/internal/config"
	"github.com/amoranio/trimple/internal/server"
)

// version is set with -ldflags "-X main.version=...".
var version = "dev"

const defaultPort = 47615

type instanceInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func main() { os.Exit(run()) }

func run() int {
	attachConsoleIfAny()
	var (
		folder    = flag.String("folder", "", "notes folder for this run (default: the saved one)")
		noBrowser = flag.Bool("no-browser", false, "do not open a window; just print the address")
		keepAlive = flag.Bool("keep-alive", false, "keep running after the window is closed")
		port      = flag.Int("port", defaultPort, "preferred port on 127.0.0.1 (falls back to any free port)")
		cfgPath   = flag.String("config", "", "settings file (default: %AppData%\\Trimple\\settings.json)")
		token     = flag.String("token", "", "fixed access token (testing only)")
		grace     = flag.Duration("grace", 20*time.Second, "exit this long after the last window closes")
		noSingle  = flag.Bool("no-single-instance", false, "always start a new server")
		showVer   = flag.Bool("version", false, "print the version and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Trimple %s\n\nUsage: trimple [flags] [note.txt ...]\n\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVer {
		fmt.Println("Trimple", version)
		return 0
	}
	server.Version = version

	cfg := *cfgPath
	if cfg == "" {
		cfg = config.DefaultPath()
	}
	cfgDir := filepath.Dir(cfg)
	_ = os.MkdirAll(cfgDir, 0o755)
	logf := newLogger(filepath.Join(cfgDir, "trimple.log"))

	tok := *token
	if tok == "" {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return fatal(logf, "cannot create a secure token: "+err.Error())
		}
		tok = hex.EncodeToString(b)
	}

	instPath := filepath.Join(cfgDir, "instance.json")
	if !*noSingle && !*noBrowser {
		if info, ok := liveInstance(instPath); ok {
			u := fmt.Sprintf("http://127.0.0.1:%d/?t=%s%s", info.Port, info.Token, openParams(*folder, flag.Args()))
			logf("Trimple is already running; opening a window on it")
			if err := openWindow(u); err != nil {
				return fatal(logf, "could not open a window: "+err.Error())
			}
			return 0
		}
	}

	srv, err := server.New(server.Options{
		Root: *folder, ConfigPath: cfg, Token: tok, KeepAlive: *keepAlive,
		Grace: *grace, Logf: logf,
	})
	if err != nil {
		return fatal(logf, err.Error())
	}
	if err := srv.Listen(*port); err != nil {
		return fatal(logf, "cannot start the local server: "+err.Error())
	}
	if !*noSingle {
		writeInstance(instPath, instanceInfo{Port: srv.Port(), Token: tok, PID: os.Getpid()})
		defer os.Remove(instPath)
	}

	u := srv.URL() + openParams("", flag.Args())
	fmt.Println("Trimple listening on", u)
	logf("listening on port %d", srv.Port())
	if !*noBrowser {
		if err := openWindow(u); err != nil {
			return fatal(logf, "could not open a window: "+err.Error()+"\nOpen this address in your browser:\n"+u)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		return fatal(logf, err.Error())
	}
	return 0
}

// openParams turns file arguments into &open=... parameters the page understands.
func openParams(_ string, args []string) string {
	var sb strings.Builder
	for _, a := range args {
		if a == "" {
			continue
		}
		abs, err := filepath.Abs(a)
		if err != nil {
			continue
		}
		sb.WriteString("&openabs=" + url.QueryEscape(abs))
	}
	return sb.String()
}

func liveInstance(path string) (instanceInfo, bool) {
	var info instanceInfo
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &info) != nil || info.Port <= 0 || info.Token == "" {
		return info, false
	}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/ping", info.Port), nil)
	req.Header.Set("X-Trimple-Token", info.Token)
	resp, err := (&http.Client{Timeout: 1500 * time.Millisecond}).Do(req)
	if err != nil {
		return info, false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return info, resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"trimple"`)
}

func writeInstance(path string, info instanceInfo) {
	b, _ := json.Marshal(info)
	_ = os.WriteFile(path, b, 0o600)
}

func fatal(logf func(string, ...any), msg string) int {
	logf("fatal: %s", msg)
	showError("Trimple", msg)
	return 1
}

// newLogger logs to a file next to the settings (and to the console when there is one).
func newLogger(path string) func(string, ...any) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 512<<10 {
		_ = os.Remove(path)
	}
	f, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	return func(format string, args ...any) {
		line := fmt.Sprintf("%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
		if f != nil {
			_, _ = f.WriteString(line)
		}
		fmt.Fprint(os.Stderr, line)
	}
}

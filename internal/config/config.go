// Package config loads and saves Trimple's settings file.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/amoranio/trimple/internal/notes"
)

// Settings are the user's preferences. Every field is validated on load and on save, because the
// file can be edited by hand and the HTTP API accepts updates.
type Settings struct {
	Folder       string   `json:"folder"`
	Theme        string   `json:"theme"`
	FontSize     int      `json:"fontSize"`
	WordWrap     bool     `json:"wordWrap"`
	PanelVisible bool     `json:"panelVisible"`
	PanelWidth   int      `json:"panelWidth"`
	GroupBy      string   `json:"groupBy"`
	ShowDone     bool     `json:"showDone"`
	OpenFiles    []string `json:"openFiles"`
}

func Default() Settings {
	return Settings{
		Theme: "dark", FontSize: 14, WordWrap: true, PanelVisible: true,
		PanelWidth: 360, GroupBy: "priority", OpenFiles: []string{},
	}
}

func clamp(v, lo, hi, fallback int) int {
	if v < lo || v > hi {
		return fallback
	}
	return v
}

// Sanitize forces every field into a safe range.
func (s *Settings) Sanitize() {
	d := Default()
	if s.Theme != "dark" && s.Theme != "light" {
		s.Theme = d.Theme
	}
	s.FontSize = clamp(s.FontSize, 8, 40, d.FontSize)
	s.PanelWidth = clamp(s.PanelWidth, 220, 900, d.PanelWidth)
	if s.GroupBy != "priority" && s.GroupBy != "label" && s.GroupBy != "file" {
		s.GroupBy = d.GroupBy
	}
	if strings.ContainsRune(s.Folder, 0) || len(s.Folder) > 1000 {
		s.Folder = ""
	}
	files := make([]string, 0, len(s.OpenFiles))
	for _, f := range s.OpenFiles {
		if len(files) >= 30 {
			break
		}
		if f != "" && len(f) <= 500 && !strings.ContainsRune(f, 0) {
			files = append(files, f)
		}
	}
	s.OpenFiles = files
}

// DefaultPath is %AppData%\Trimple\settings.json on Windows.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "Trimple", "settings.json")
}

// Load never fails: a missing or corrupt file gives the defaults.
func Load(path string) Settings {
	s := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Default()
	}
	s.Sanitize()
	return s
}

// Save writes the settings atomically.
func Save(path string, s Settings) error {
	defer notes.LockPath(path)()
	s.Sanitize()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return notes.WriteFileAtomic(path, b)
}

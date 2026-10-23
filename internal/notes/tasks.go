package notes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Task is one "- [ ] text !1 #label" line.
type Task struct {
	Path     string   `json:"path"` // relative to the notes folder, forward slashes
	Line     int      `json:"line"` // 1-based
	Done     bool     `json:"done"`
	Priority int      `json:"priority"` // 0 = none, 1 high .. 3 low
	Labels   []string `json:"labels"`
	Text     string   `json:"text"`
	Raw      string   `json:"raw"` // the exact line, used to detect that it moved before editing
}

var (
	taskRe  = regexp.MustCompile(`^([ \t]*[-*+][ \t]+\[)([ xX])(\][ \t]+)(.*)$`)
	fenceRe = regexp.MustCompile("^\\s*(`{3,}|~{3,})(.*)$")
)

// Sentinel errors callers can map to user-facing messages.
var (
	ErrStale    = errors.New("the task changed on disk; refresh and try again")
	ErrReadOnly = errors.New("this file cannot be rewritten safely")
	ErrNoTask   = errors.New("that line is no longer a task")
)

// splitLines splits text into lines and the separators between them (len(seps) == len(lines)-1).
func splitLines(text string) (lines, seps []string) {
	start := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			lines = append(lines, text[start:i])
			seps = append(seps, "\n")
			start = i + 1
		case '\r':
			end := i
			sep := "\r"
			if i+1 < len(text) && text[i+1] == '\n' {
				sep = "\r\n"
				i++
			}
			lines = append(lines, text[start:end])
			seps = append(seps, sep)
			start = i + 1
		}
	}
	lines = append(lines, text[start:])
	return lines, seps
}

func isLabelRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.M, r) || r == '_' || r == '-'
}

// parseWord recognises "#label" (optionally followed by one punctuation mark that stays in the text).
func parseLabel(w string) (label, rest string, ok bool) {
	if len(w) < 2 || w[0] != '#' {
		return "", "", false
	}
	body := w[1:]
	first := []rune(body)[0]
	if !unicode.IsLetter(first) { // "#123" is an issue number, not a label
		return "", "", false
	}
	end := 0
	for i, r := range body {
		if !isLabelRune(r) {
			break
		}
		end = i + len(string(r))
	}
	rest = body[end:]
	if rest != "" && !strings.ContainsRune(",;.!?)", []rune(rest)[0]) {
		return "", "", false
	}
	return strings.ToLower(body[:end]), rest, true
}

func parseBody(body string) (priority int, labels []string, text string) {
	labels = []string{}
	kept := make([]string, 0, 8)
	for _, w := range strings.Fields(body) {
		switch {
		case w == "!1" || w == "!2" || w == "!3":
			if priority == 0 {
				priority = int(w[1] - '0')
			}
		default:
			if l, rest, ok := parseLabel(w); ok {
				dup := false
				for _, e := range labels {
					if e == l {
						dup = true
						break
					}
				}
				if !dup {
					labels = append(labels, l)
				}
				if rest != "" {
					kept = append(kept, rest)
				}
			} else {
				kept = append(kept, w)
			}
		}
	}
	text = strings.Join(kept, " ")
	if text == "" {
		text = strings.TrimSpace(body)
	}
	return priority, labels, text
}

// ParseTaskLine parses a single line; ok is false when it is not a task.
func ParseTaskLine(line string) (t Task, ok bool) {
	m := taskRe.FindStringSubmatch(line)
	if m == nil || strings.TrimSpace(m[4]) == "" {
		return Task{}, false
	}
	p, labels, text := parseBody(m[4])
	return Task{Done: m[2] != " ", Priority: p, Labels: labels, Text: text, Raw: line}, true
}

// ParseTasks extracts every task from text, skipping fenced code blocks.
func ParseTasks(text, relPath string) []Task {
	lines, _ := splitLines(text)
	var tasks []Task
	fence := ""
	for i, line := range lines {
		if fence != "" || strings.ContainsAny(line, "`~") {
			if m := fenceRe.FindStringSubmatch(line); m != nil {
				f := m[1]
				if fence == "" {
					fence = f
					continue
				}
				if f[0] == fence[0] && len(f) >= len(fence) && strings.TrimSpace(m[2]) == "" {
					fence = ""
					continue
				}
			}
			if fence != "" {
				continue
			}
		}
		if !strings.Contains(line, "[") {
			continue
		}
		if t, ok := ParseTaskLine(line); ok {
			t.Path = relPath
			t.Line = i + 1
			tasks = append(tasks, t)
		}
	}
	return tasks
}

// SetTaskDone flips one checkbox in a file. Only that single character changes: encoding, BOM,
// line endings and every other byte are written back exactly as they were. It refuses when the
// file cannot be rewritten losslessly or when the line is not what the caller last saw.
func SetTaskDone(path string, line int, done bool, expectedRaw *string) error {
	f, err := ReadFile(path)
	if err != nil {
		return err
	}
	if !f.Lossless {
		return fmt.Errorf("%w: it %s", ErrReadOnly, f.Problem)
	}
	lines, seps := splitLines(f.Text)
	if line < 1 || line > len(lines) {
		return fmt.Errorf("%w: line %d no longer exists", ErrStale, line)
	}
	raw := lines[line-1]
	if expectedRaw != nil && raw != *expectedRaw {
		return ErrStale
	}
	m := taskRe.FindStringSubmatchIndex(raw)
	if m == nil {
		return ErrNoTask
	}
	mark := m[4] // start of the [ xX] group
	c := byte(' ')
	if done {
		c = 'x'
	}
	lines[line-1] = raw[:mark] + string(c) + raw[mark+1:]

	var sb strings.Builder
	for i, l := range lines {
		sb.WriteString(l)
		if i < len(seps) {
			sb.WriteString(seps[i])
		}
	}
	enc, kind := Encode(sb.String(), f.Kind)
	if kind != f.Kind {
		return ErrReadOnly // cannot happen for a lossless file; never change encoding on a tick
	}
	return WriteFileAtomic(path, enc)
}

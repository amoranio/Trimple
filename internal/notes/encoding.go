// Package notes holds everything that touches the user's .txt files: encoding detection,
// task parsing, safe rewriting and the folder index. It has no UI or HTTP code.
package notes

import (
	"bytes"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Kind is the on-disk text encoding of a file.
type Kind string

const (
	UTF8    Kind = "utf8"
	UTF8BOM Kind = "utf8bom"
	UTF16LE Kind = "utf16le"
	UTF16BE Kind = "utf16be"
	ANSI    Kind = "ansi" // Windows-1252, what old Notepad wrote
)

// cp1252High maps bytes 0x80..0x9F. Undefined bytes map to the same C1 code point,
// so every byte value survives a decode/encode round trip.
var cp1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

var cp1252Reverse = func() map[rune]byte {
	m := make(map[rune]byte, 32)
	for i, r := range cp1252High {
		m[r] = byte(0x80 + i)
	}
	return m
}()

func decodeANSI(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		switch {
		case c < 0x80:
			sb.WriteByte(c)
		case c < 0xA0:
			sb.WriteRune(cp1252High[c-0x80])
		default:
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

// encodeANSI reports false when the text contains a character Windows-1252 cannot hold.
func encodeANSI(s string) ([]byte, bool) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case r >= 0xA0 && r <= 0xFF:
			out = append(out, byte(r))
		default:
			c, ok := cp1252Reverse[r]
			if !ok {
				return nil, false
			}
			out = append(out, c)
		}
	}
	return out, true
}

func decodeUTF16(b []byte, bigEndian bool) string {
	n := len(b) / 2 // a dangling odd byte is dropped; the round-trip check will notice
	u := make([]uint16, n)
	for i := 0; i < n; i++ {
		if bigEndian {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i+1])<<8 | uint16(b[2*i])
		}
	}
	return string(utf16.Decode(u))
}

func encodeUTF16(s string, bigEndian bool, bom []byte) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(bom)+2*len(u))
	out = append(out, bom...)
	for _, v := range u {
		if bigEndian {
			out = append(out, byte(v>>8), byte(v))
		} else {
			out = append(out, byte(v), byte(v>>8))
		}
	}
	return out
}

// Decode detects the encoding of raw file bytes and returns the text.
func Decode(b []byte) (string, Kind) {
	switch {
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:]), UTF8BOM
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return decodeUTF16(b[2:], false), UTF16LE
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return decodeUTF16(b[2:], true), UTF16BE
	}
	if utf8.Valid(b) {
		return string(b), UTF8
	}
	return decodeANSI(b), ANSI
}

// Encode turns text back into bytes. If an ANSI file now holds characters Windows-1252 cannot
// represent, the result is UTF-8 and the returned Kind says so (callers must ask the user first).
func Encode(text string, kind Kind) ([]byte, Kind) {
	switch kind {
	case UTF8BOM:
		return append([]byte{0xEF, 0xBB, 0xBF}, text...), UTF8BOM
	case UTF16LE:
		return encodeUTF16(text, false, []byte{0xFF, 0xFE}), UTF16LE
	case UTF16BE:
		return encodeUTF16(text, true, []byte{0xFE, 0xFF}), UTF16BE
	case ANSI:
		if b, ok := encodeANSI(text); ok {
			return b, ANSI
		}
		return []byte(text), UTF8
	default:
		return []byte(text), UTF8
	}
}

// Check reports whether rewriting this text would reproduce the original bytes exactly
// (and be safe to show in an editor). When it would not, the file must stay read-only.
func Check(orig []byte, text string, kind Kind) (ok bool, problem string) {
	if strings.IndexByte(text, 0) >= 0 {
		return false, "contains NUL characters"
	}
	if (kind == UTF8 || kind == UTF8BOM) && !utf8.ValidString(text) {
		return false, "contains bytes that are not valid UTF-8"
	}
	enc, k := Encode(text, kind)
	if k != kind || !bytes.Equal(enc, orig) {
		return false, "contains bytes that are not valid for its encoding"
	}
	return true, ""
}

// DominantEOL returns the most common line ending; ties and files with no newline give CRLF.
func DominantEOL(text string) string {
	crlf := strings.Count(text, "\r\n")
	lf := strings.Count(text, "\n") - crlf
	cr := strings.Count(text, "\r") - crlf
	if lf > crlf && lf >= cr {
		return "\n"
	}
	if cr > crlf && cr > lf {
		return "\r"
	}
	return "\r\n"
}

// NormalizeNewlines converts every line ending to "\n" (what a browser textarea uses).
func NormalizeNewlines(s string) string {
	if strings.IndexByte(s, '\r') < 0 {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// ApplyEOL converts "\n"-normalised text to the given line ending.
func ApplyEOL(s, eol string) string {
	s = NormalizeNewlines(s)
	if eol == "\n" {
		return s
	}
	return strings.ReplaceAll(s, "\n", eol)
}

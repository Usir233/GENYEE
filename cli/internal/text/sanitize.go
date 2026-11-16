package text

import (
	"strings"
	"unicode/utf8"
)

// SanitizeTerminal strips ANSI/VT escape sequences (CSI, OSC including OSC 52
// clipboard payloads, DCS, SOS, PM, APC, 2-byte/nF escapes), C0/DEL/C1 control
// characters, and Unicode bidirectional override/isolate controls from
// untrusted strings before rendering to human-readable output.
//
// Preserves valid UTF-8 text, '\n' (normalizing "\r\n" to "\n" and converting
// bare '\r' to ' ' to prevent carriage-return line-overwrite spoofing), and '\t'.
func SanitizeTerminal(s string) string {
	if s == "" {
		return ""
	}
	if !needsTerminalSanitize(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))

	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		if c == 0x1b {
			i = skipEscapeSeq(s, i+1)
			continue
		}
		if c == '\r' {
			if i+1 < n && s[i+1] == '\n' {
				b.WriteByte('\n')
				i += 2
			} else {
				b.WriteByte(' ')
				i++
			}
			continue
		}
		if c < 0x80 {
			if c == '\n' || c == '\t' || (c >= 0x20 && c != 0x7f) {
				b.WriteByte(c)
			}
			i++
			continue
		}
		r, width := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && width == 1 {
			b.WriteRune('\uFFFD')
			i++
			continue
		}
		if r == 0x9b {
			i = skipCSIBody(s, i+width)
			continue
		}
		if r == 0x90 || r == 0x98 || r == 0x9d || r == 0x9e || r == 0x9f {
			i = skipControlString(s, i+width)
			continue
		}
		if isUnsafeRune(r) {
			i += width
			continue
		}
		b.WriteRune(r)
		i += width
	}
	return b.String()
}

// SanitizeInline strips terminal escape/control sequences and collapses
// newlines, carriage returns, and tabs to single spaces for single-line fields.
func SanitizeInline(s string) string {
	if s == "" {
		return ""
	}
	return lineReplacer.Replace(SanitizeTerminal(s))
}

func needsTerminalSanitize(s string) bool {
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x20 {
			if c != '\n' && c != '\t' {
				return true
			}
			i++
			continue
		}
		if c == 0x7f {
			return true
		}
		if c < 0x80 {
			i++
			continue
		}
		r, width := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && width == 1 {
			return true
		}
		if isUnsafeRune(r) {
			return true
		}
		i += width
	}
	return false
}

func isUnsafeRune(r rune) bool {
	if r >= 0x80 && r <= 0x9f {
		return true
	}
	if r >= 0x202a && r <= 0x202e {
		return true
	}
	if r >= 0x2066 && r <= 0x2069 {
		return true
	}
	return false
}

func skipEscapeSeq(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return skipCSIBody(s, i+1)
	case ']', 'P', 'X', '^', '_':
		return skipControlString(s, i+1)
	default:
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
			i++
		}
		return i
	}
}

func skipCSIBody(s string, i int) int {
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
		i++
	}
	if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
		i++
	}
	return i
}

func skipControlString(s string, i int) int {
	for i < len(s) {
		if s[i] == 0x07 {
			return i + 1
		}
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
			return i + 2
		}
		if s[i] >= 0x80 {
			r, width := utf8.DecodeRuneInString(s[i:])
			if r == 0x9c {
				return i + width
			}
			i += width
			continue
		}
		i++
	}
	return i
}

package format

import "github.com/Usir233/GENYEE/cli/internal/text"

// DashIfEmpty returns "-" for empty strings, otherwise terminal-sanitized s.
// Standard rendering convention for human-readable table cells whose value is
// optional (e.g. user email in `auth list` / `profile list`).
func DashIfEmpty(s string) string {
	s = text.SanitizeInline(s)
	if s == "" {
		return "-"
	}
	return s
}

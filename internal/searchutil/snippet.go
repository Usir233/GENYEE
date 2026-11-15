package searchutil

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ExtractSnippet matches Agent wiki_search: ~60 runes of context around the
// first case-insensitive regex hit, match itself capped at 100. Invalid
// patterns return an empty string.
func ExtractSnippet(content string, query string) string {
	if content == "" || query == "" {
		return ""
	}
	re, err := regexp.Compile("(?i)" + query)
	if err != nil {
		return ""
	}
	loc := re.FindStringIndex(content)
	if loc == nil {
		return ""
	}

	beforeStr := takeTrailingRunes(content[:loc[0]], 60)
	matchStr, matchTruncated := takeLeadingRunes(content[loc[0]:loc[1]], 100)
	if matchTruncated {
		matchStr += "..."
	}
	afterStr, _ := takeLeadingRunes(content[loc[1]:], 60)

	snippet := beforeStr + matchStr + afterStr
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	for strings.Contains(snippet, "  ") {
		snippet = strings.ReplaceAll(snippet, "  ", " ")
	}

	return "... " + strings.TrimSpace(snippet) + " ..."
}

func takeLeadingRunes(s string, maxRunes int) (string, bool) {
	if maxRunes <= 0 || s == "" {
		return "", s != "" && maxRunes <= 0
	}
	var b strings.Builder
	count := 0
	for idx := 0; idx < len(s); {
		r, size := utf8.DecodeRuneInString(s[idx:])
		if count == maxRunes {
			return b.String(), true
		}
		b.WriteRune(r)
		count++
		idx += size
	}
	return b.String(), false
}

func takeTrailingRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return ""
	}
	buf := make([]rune, 0, maxRunes)
	for idx := len(s); idx > 0 && len(buf) < maxRunes; {
		r, size := utf8.DecodeLastRuneInString(s[:idx])
		buf = append(buf, r)
		idx -= size
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

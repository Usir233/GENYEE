package utils

import (
	"regexp"
	"strings"
)

// shellAssignmentPattern finds the start of NAME=value in a model-built command.
// The name must be UPPER_SNAKE_CASE so flags like --model and URLs are not
// treated as environment variables.
var shellAssignmentPattern = regexp.MustCompile(
	`^([A-Z_][A-Z0-9_]{0,127})=`,
)

var embeddedShellAssignmentPattern = regexp.MustCompile(`(?:^|[;|&\s(])[A-Z_][A-Z0-9_]{0,127}=`)

// MaskCommandAssignments replaces the value of every NAME=value assignment
// with a placeholder. A command is logged at Info, and passing a credential
// inline is a documented way to hand a skill its key, so the raw string must
// never reach the log.
func MaskCommandAssignments(command string) string {
	var masked strings.Builder
	previous := 0
	for _, assignment := range shellAssignments(command) {
		masked.WriteString(command[previous:assignment.start])
		masked.WriteString("***")
		previous = assignment.end
	}
	masked.WriteString(command[previous:])
	return masked.String()
}

// ExtractShellAssignments returns statically known NAME=value pairs. Shell
// expansion must never run here, and an expression is not a reusable credential.
func ExtractShellAssignments(command string) map[string]string {
	out := map[string]string{}
	for _, assignment := range shellAssignments(command) {
		if !assignment.capture {
			continue
		}
		if assignment.literal && assignment.value != "" {
			out[assignment.name] = assignment.value
		} else {
			// A later dynamic/empty assignment supersedes an earlier literal.
			delete(out, assignment.name)
		}
	}
	return out
}

type shellAssignment struct {
	name, value string
	start, end  int
	literal     bool
	capture     bool
}

func shellAssignments(command string) []shellAssignment {
	var assignments []shellAssignment
	captureAllowed, exporting := true, false
	heredoc := false
	for offset := 0; offset < len(command); {
		if command[offset] == '#' {
			// A comment starts only at a word boundary. Its semicolons must
			// not reopen credential capture; hide its text conservatively.
			end := strings.IndexByte(command[offset:], '\n')
			if end < 0 {
				end = len(command)
			} else {
				end += offset
			}
			assignments = append(assignments, shellAssignment{start: offset + 1, end: end})
			offset = end
			continue
		}
		if strings.ContainsRune(" \t\n;|&()<>", rune(command[offset])) {
			switch command[offset] {
			case '\n', ';', '|', '&', '(':
				captureAllowed, exporting = true, false
			case '<', '>':
				// Do not infer reusable credentials from redirected input or a
				// here-document. Its contents have a different language/context.
				captureAllowed, exporting = false, false
				if command[offset] == '<' && offset+1 < len(command) && command[offset+1] == '<' {
					heredoc = true
				}
			}
			offset++
			continue
		}
		match := shellAssignmentPattern.FindStringSubmatchIndex(command[offset:])
		start := offset
		if match != nil {
			start += match[1]
		}
		end, value, literal, opaque := shellAssignmentWord(command, start)
		switch {
		case match != nil:
			assignments = append(assignments, shellAssignment{
				name: command[offset+match[2] : offset+match[3]], value: value,
				start: start, end: end, literal: literal, capture: captureAllowed && !heredoc,
			})
		case opaque || embeddedShellAssignmentPattern.MatchString(value):
			// A quoted argument may be a child shell script. Hide the whole
			// word when it contains assignments, but never capture it as the
			// caller's environment (it may instead be ordinary note text).
			assignments = append(assignments, shellAssignment{start: offset, end: end})
		case captureAllowed && value == "export":
			exporting = true
		case !exporting && value != "env":
			captureAllowed = false
		}
		offset = end
	}
	return assignments
}

// shellAssignmentWord reads a literal shell word, including adjacent quoted
// pieces and escaped separators. Unsupported expansions are not interpreted:
// omit the remaining command from the display rather than risk leaking a secret
// through a delimiter nested inside an expression. Execution uses the original.
func shellAssignmentWord(command string, start int) (end int, value string, literal, opaque bool) {
	var decoded strings.Builder
	var quote byte
	literal = true
	for end = start; end < len(command); end++ {
		c := command[end]
		isProcSub := (c == '<' || c == '>') && end+1 < len(command) && command[end+1] == '('
		if quote == 0 && (c == '(' && end == start || isProcSub) {
			return len(command), "", false, true
		}
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			} else {
				decoded.WriteByte(c)
			}
			continue
		}
		if c == '\\' {
			if end+1 == len(command) {
				return len(command), "", false, true
			}
			next := command[end+1]
			if quote == '"' && !strings.ContainsRune("$`\"\\\n", rune(next)) {
				decoded.WriteByte(c)
			}
			if next != '\n' {
				decoded.WriteByte(next)
			}
			end++
			continue
		}
		if c == '"' {
			if quote == '"' {
				quote = 0
			} else {
				quote = '"'
			}
			continue
		}
		if c == '\'' && quote == 0 {
			quote = '\''
			continue
		}
		if c == '`' || c == '$' && end+1 < len(command) && strings.ContainsRune("([{'\"", rune(command[end+1])) {
			return len(command), "", false, true
		}
		if c == '$' || c == '~' && quote == 0 && (end == start || command[end-1] == ':') {
			literal = false
		}
		if quote == 0 && strings.ContainsRune(" \t\n;|&()<>", rune(c)) {
			break
		}
		decoded.WriteByte(c)
	}
	return end, decoded.String(), literal && quote == 0, quote != 0
}

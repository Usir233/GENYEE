package client

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

const maxSSEEventBytes = 4 << 20

var errSSEEventTooLarge = fmt.Errorf("SSE event data exceeds the %d MiB limit", maxSSEEventBytes>>20)

type sseEventBuffer struct {
	b strings.Builder
}

func (s *sseEventBuffer) appendLine(line string) error {
	payload := strings.TrimPrefix(line[5:], " ")
	if s.b.Len()+len(payload)+1 > maxSSEEventBytes {
		return errSSEEventTooLarge
	}
	s.b.WriteString(payload)
	s.b.WriteByte('\n')
	return nil
}

func (s *sseEventBuffer) complete() string {
	return completeSSEData(s.b.String())
}

func (s *sseEventBuffer) reset() {
	s.b.Reset()
}

func (s *sseEventBuffer) incompleteError() error {
	return incompleteSSEDataError(s.b.String())
}

func parseSSEEventField(line string) (string, bool) {
	if line == "event" || strings.HasPrefix(line, "event:") {
		_, value, _ := strings.Cut(line, ":")
		return strings.TrimPrefix(value, " "), true
	}
	return "", false
}

func isDefaultSSEEvent(eventType string) bool {
	return eventType == "" || eventType == "message"
}

func appendSSEDataLine(buffer, line string) string {
	return buffer + strings.TrimPrefix(line[5:], " ") + "\n"
}

func completeSSEData(buffer string) string {
	return strings.TrimSuffix(buffer, "\n")
}

// A data frame is dispatched only by its blank line. Do not silently report
// success if the connection ended with undispatched data.
func incompleteSSEDataError(buffer string) error {
	if completeSSEData(buffer) == "" {
		return nil
	}
	return fmt.Errorf("incomplete SSE data frame: %w", io.ErrUnexpectedEOF)
}

// newSSELineSplitter accepts all SSE line endings: CRLF, LF and bare CR.
// Consume CR immediately so a blank line dispatches without waiting for EOF
// or the next event. Remember it only to swallow a following LF, even when
// that LF arrives in a separate network read.
func newSSELineSplitter() bufio.SplitFunc {
	firstLine := true
	skipLF := false
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if skipLF && len(data) > 0 {
			skipLF = false
			if data[0] == '\n' {
				advance = 1
				data = data[1:]
			}
		}
		if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
			skipLF = data[i] == '\r'
			advance += i + 1
			token = data[:i]
		} else if atEOF && len(data) > 0 {
			advance += len(data)
			token = data
		}
		// SSE ignores one BOM at the start of the stream. Wait for the
		// first token so a BOM split across reads is handled the same way.
		if firstLine && token != nil {
			token = bytes.TrimPrefix(token, []byte("\ufeff"))
			firstLine = false
		}
		return advance, token, nil
	}
}

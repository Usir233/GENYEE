package logger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/utils"
)

var llmDebug struct {
	mu        sync.Mutex
	enabled   bool
	dir       string
	cleanupWG sync.WaitGroup
}

func init() {
	configureLLMDebugLog()
}

func configureLLMDebugLog() {
	val := strings.TrimSpace(os.Getenv("LLM_DEBUG_LOG"))
	if val == "" || strings.EqualFold(val, "false") || val == "0" {
		llmDebug.mu.Lock()
		llmDebug.enabled = false
		llmDebug.mu.Unlock()
		llmDebug.cleanupWG.Wait()
		return
	}

	var dir string
	if strings.EqualFold(val, "true") || val == "1" {
		dir = resolveLLMDebugDir()
	} else {
		dir = val
	}
	if dir == "" {
		dir = "llm_debug"
	}

	// Records hold full prompts and model replies: keep them to the service user.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "llm_debug: failed to create dir %s: %v\n", dir, err)
		return
	}

	llmDebug.mu.Lock()
	llmDebug.dir = dir
	llmDebug.enabled = true
	llmDebug.cleanupWG.Add(1)
	llmDebug.mu.Unlock()

	go func(targetDir string) {
		defer llmDebug.cleanupWG.Done()
		cleanupOldDebugFiles(targetDir, 7*24*time.Hour)
	}(dir)

	fmt.Fprintf(os.Stderr, "llm_debug: LLM debug log enabled → %s/\n", dir)
}

func resolveLLMDebugDir() string {
	if logPath := strings.TrimSpace(os.Getenv("LOG_PATH")); logPath != "" {
		return filepath.Join(filepath.Dir(logPath), "llm_debug")
	}
	if macPath := defaultMacAppLogPath(); macPath != "" {
		return filepath.Join(filepath.Dir(macPath), "llm_debug")
	}
	return "llm_debug"
}

// cleanupOldDebugFiles removes files older than maxAge from the debug directory.
func cleanupOldDebugFiles(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			target := filepath.Join(dir, e.Name())
			if rmErr := os.Remove(target); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "llm_debug: remove expired %s: %v\n", target, rmErr)
			}
		}
	}
}

// LLMDebugEnabled returns true when the dedicated LLM debug log is active.
func LLMDebugEnabled() bool {
	llmDebug.mu.Lock()
	enabled := llmDebug.enabled
	llmDebug.mu.Unlock()
	return enabled
}

// LLMDebugLog writes a complete model call record to a per-request log file.
// All calls sharing the same request_id are appended to the same file.
func LLMDebugLog(ctx context.Context, record *LLMCallRecord) {
	if record == nil || !LLMDebugEnabled() {
		return
	}

	text := formatRecord(record)

	reqID := extractRequestID(ctx)
	filename := buildFilename(reqID)

	llmDebug.mu.Lock()
	defer llmDebug.mu.Unlock()
	if !llmDebug.enabled {
		return
	}

	path := filepath.Join(llmDebug.dir, filename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llm_debug: open %s: %v\n", path, err)
		return
	}
	_, writeErr := f.WriteString(text)
	closeErr := f.Close()
	if writeErr != nil {
		fmt.Fprintf(os.Stderr, "llm_debug: write %s: %v\n", path, writeErr)
	}
	if closeErr != nil {
		fmt.Fprintf(os.Stderr, "llm_debug: close %s: %v\n", path, closeErr)
	}
}

// maxDebugFilenameID bounds the request-id part of a debug file name, well
// inside the 255-byte name limit of common filesystems.
const maxDebugFilenameID = 128

// buildFilename derives the debug file name from the request id. The id comes
// from the client's X-Request-ID header, so everything outside a plain
// identifier alphabet is replaced: a "../" in it must not pick the directory
// the record is written to.
func buildFilename(reqID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, reqID)
	if len(safe) > maxDebugFilenameID {
		safe = safe[:maxDebugFilenameID]
	}
	if strings.Trim(safe, "_") != "" {
		return safe + ".log"
	}
	return time.Now().Format("20060102_150405.000") + ".log"
}

func formatRecord(r *LLMCallRecord) string {
	var b strings.Builder
	b.Grow(4096)

	separator := fmt.Sprintf("================ %s ================", r.CallType)
	b.WriteString("\n")
	b.WriteString(separator)
	b.WriteString("\n")
	fmt.Fprintf(&b, "Time:     %s\n", time.Now().Format("2006-01-02 15:04:05.000"))
	fmt.Fprintf(&b, "Model:    %s\n", utils.RedactSensitiveString(r.Model))
	if r.Duration > 0 {
		fmt.Fprintf(&b, "Duration: %s\n", r.Duration.Round(time.Millisecond))
	}

	for _, s := range r.Sections {
		fmt.Fprintf(&b, "\n---------- %s ----------\n", s.Title)
		redactedContent := utils.RedactSensitiveString(s.Content)
		b.WriteString(redactedContent)
		if !strings.HasSuffix(redactedContent, "\n") {
			b.WriteString("\n")
		}
	}

	if r.Error != "" {
		b.WriteString("\n---------- Error ----------\n")
		b.WriteString(utils.RedactSensitiveString(r.Error))
		b.WriteString("\n")
	}

	b.WriteString(strings.Repeat("=", len(separator)))
	b.WriteString("\n")

	return b.String()
}

func extractRequestID(ctx context.Context) string {
	entry := GetLogger(ctx)
	if v, ok := entry.Data["request_id"]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// LLMCallRecord holds all information for one model API call.
type LLMCallRecord struct {
	CallType string // "Chat", "Chat Stream", "Embedding", "Rerank", "VLM"
	Model    string
	Duration time.Duration
	Sections []RecordSection
	Error    string
}

// RecordSection is a titled block of text within a call record.
type RecordSection struct {
	Title   string
	Content string
}

// LLMMessage is a simplified chat message for logging.
type LLMMessage struct {
	Role       string
	Content    string
	Name       string
	ToolCallID string
	Images     []string
	ToolCalls  []LLMToolCallInfo
}

// LLMToolCallInfo holds tool call info for logging.
type LLMToolCallInfo struct {
	ID        string
	FuncName  string
	Arguments string
}

// FormatMessages formats chat messages into a readable block.
func FormatMessages(messages []LLMMessage) string {
	var b strings.Builder
	for _, m := range messages {
		fmt.Fprintf(&b, "[%s]", m.Role)
		if m.Name != "" {
			fmt.Fprintf(&b, " name=%s", m.Name)
		}
		if m.ToolCallID != "" {
			fmt.Fprintf(&b, " tool_call_id=%s", m.ToolCallID)
		}
		b.WriteString("\n")

		if m.Content != "" {
			b.WriteString(utils.RedactSensitiveString(m.Content))
			b.WriteString("\n")
		}
		for _, img := range m.Images {
			redactedImg := utils.RedactSensitiveString(img)
			if len([]rune(redactedImg)) > 80 {
				fmt.Fprintf(&b, "[image: %s (%d bytes)]\n", TruncateRunes(redactedImg, 80), len(redactedImg))
			} else {
				fmt.Fprintf(&b, "[image: %s]\n", redactedImg)
			}
		}
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "  -> tool_call: id=%s, func=%s, args=%s\n",
				tc.ID, tc.FuncName, utils.RedactSensitiveString(tc.Arguments))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// FormatToolCalls formats response tool calls into a readable block.
func FormatToolCalls(tcs []LLMToolCallInfo) string {
	var b strings.Builder
	for _, tc := range tcs {
		fmt.Fprintf(&b, "[tool_call] id=%s, func=%s\n%s\n\n",
			tc.ID, tc.FuncName, utils.RedactSensitiveString(tc.Arguments))
	}
	return b.String()
}

// TruncateRunes truncates a string to maxRunes runes, appending "..." if truncated.
// This is safe for multi-byte UTF-8 characters (e.g. Chinese).
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

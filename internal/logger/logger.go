// Package logger provides structured, redacted application and LLM debug logging.
package logger

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
	"gopkg.in/natefinch/lumberjack.v2"
)

// appLogger uses a private instance so external dependencies mutating global
// logrus state cannot drop or redirect application logs.
var appLogger = logrus.New()

var (
	loggerMu      sync.Mutex
	activeLogFile io.WriteCloser
	ansiEscapeRE  = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
)

// ansiStripWriter removes ANSI color/style sequences so file logs stay plain text
// while stdout can still render colors in a terminal.
type ansiStripWriter struct {
	w io.Writer
}

func (s *ansiStripWriter) Write(p []byte) (int, error) {
	_, err := s.w.Write(ansiEscapeRE.ReplaceAll(p, nil))
	return len(p), err
}

// LogLevel represents a structured logging severity level.
type LogLevel string

// Supported log levels.
const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
	LevelFatal LogLevel = "fatal"
)

// ANSI color codes.
const (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorWhite  = "\033[37m"
	colorGray   = "\033[90m"
	colorReset  = "\033[0m"
)

// CustomFormatter formats logrus entries with automatic secret redaction,
// control-character sanitization, and optional template/ANSI color support.
type CustomFormatter struct {
	ForceColor bool   // Whether to emit ANSI colors even when stdout is not a TTY.
	Template   string // Optional GENYEE_LOG_FORMAT template; empty uses built-in format.

	// threadNeeded caches whether Template references %thread to avoid runtime.Stack per line.
	threadNeeded bool
}

func levelColorFor(level logrus.Level) string {
	switch level {
	case logrus.DebugLevel:
		return colorCyan
	case logrus.InfoLevel:
		return colorGreen
	case logrus.WarnLevel:
		return colorYellow
	case logrus.ErrorLevel:
		return colorRed
	case logrus.FatalLevel:
		return colorPurple
	}
	return ""
}

func sanitizeLogToken(s string) string {
	var b strings.Builder
	sanitized := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < 0x20 || ch == 0x7f {
			if !sanitized {
				b.Grow(len(s))
				b.WriteString(s[:i])
				sanitized = true
			}
			b.WriteByte('_')
			continue
		}
		if sanitized {
			b.WriteByte(ch)
		}
	}
	if !sanitized {
		return s
	}
	return b.String()
}

func sanitizeAndRedactLogValue(key string, val interface{}) string {
	if utils.IsSensitiveLogKey(key) {
		if s, ok := val.(string); ok && strings.TrimSpace(s) == "" {
			return ""
		}
		if val == nil {
			return "<nil>"
		}
		return "[REDACTED]"
	}
	raw := fmt.Sprintf("%v", val)
	return sanitizeLogToken(utils.RedactSensitiveString(raw))
}

func sanitizeAndRedactMessage(msg string) string {
	return sanitizeLogToken(utils.RedactSensitiveString(msg))
}

// Format renders a single logrus entry into bytes.
func (f *CustomFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	timestamp := entry.Time.Format("2006-01-02 15:04:05.000")
	level := strings.ToUpper(entry.Level.String())

	callerRaw, _ := entry.Data["caller"].(string)
	caller := sanitizeLogToken(callerRaw)
	var traceID string
	hasRequestID := false
	if rawReqID, ok := entry.Data["request_id"]; ok {
		hasRequestID = true
		if s, ok := rawReqID.(string); ok {
			traceID = sanitizeLogToken(utils.RedactSensitiveString(s))
		} else {
			traceID = sanitizeLogToken(utils.RedactSensitiveString(fmt.Sprintf("%v", rawReqID)))
		}
	}

	keys := make([]string, 0, len(entry.Data))
	for k := range entry.Data {
		if k != "caller" && k != "request_id" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	safeMessage := sanitizeAndRedactMessage(entry.Message)

	if f.Template != "" {
		msg := safeMessage
		for _, k := range keys {
			cleanKey := sanitizeLogToken(k)
			msg += fmt.Sprintf(" %s=%s", cleanKey, sanitizeAndRedactLogValue(k, entry.Data[k]))
		}
		shortCaller := caller
		if len(shortCaller) > 50 {
			shortCaller = shortCaller[len(shortCaller)-50:]
		}
		thread := ""
		if f.threadNeeded {
			thread = getGoroutineID()
		}
		levelOut := level
		if f.ForceColor {
			if c := levelColorFor(entry.Level); c != "" {
				levelOut = c + level + colorReset
			}
		}
		r := strings.NewReplacer(
			"%d", timestamp,
			"%level", levelOut,
			"%thread", thread,
			"%logger", shortCaller,
			"%traceId", traceID,
			"%msg", msg,
		)
		return []byte(r.Replace(f.Template) + "\n"), nil
	}

	var levelColor, resetColor string
	if f.ForceColor {
		levelColor = levelColorFor(entry.Level)
		if levelColor == "" {
			levelColor = colorReset
		}
		resetColor = colorReset
	}

	fields := ""
	if hasRequestID {
		if f.ForceColor {
			fields += fmt.Sprintf("%s%s%s ", colorBlue, traceID, colorReset)
		} else {
			fields += fmt.Sprintf("%s ", traceID)
		}
	}

	for _, k := range keys {
		cleanKey := sanitizeLogToken(k)
		val := sanitizeAndRedactLogValue(k, entry.Data[k])
		if f.ForceColor {
			coloredVal := fmt.Sprintf("%s%s%s", colorWhite, val, colorReset)
			if k == "error" {
				coloredVal = fmt.Sprintf("%s%s%s", colorRed, val, colorReset)
			}
			fields += fmt.Sprintf("%s%s%s=%s ", colorCyan, cleanKey, colorReset, coloredVal)
		} else {
			fields += fmt.Sprintf("%s=%s ", cleanKey, val)
		}
	}

	fields = strings.TrimSpace(fields)

	if f.ForceColor {
		coloredTimestamp := fmt.Sprintf("%s%s%s", colorGray, timestamp, resetColor)
		coloredCaller := caller
		if caller != "" {
			coloredCaller = fmt.Sprintf("%s%s%s", colorPurple, caller, resetColor)
		}
		return []byte(fmt.Sprintf("%s%-5s%s[%s] [%s] %-20s | %s\n",
			levelColor, level, resetColor, coloredTimestamp, fields, coloredCaller, safeMessage)), nil
	}

	return []byte(fmt.Sprintf("%-5s[%s] [%s] %-20s | %s\n",
		level, timestamp, fields, caller, safeMessage)), nil
}

func getGoroutineID() string {
	buf := make([]byte, 64)
	buf = buf[:runtime.Stack(buf, false)]
	i := 0
	for i < len(buf) && buf[i] != ' ' {
		i++
	}
	if i >= len(buf) {
		return "0"
	}
	buf = buf[i+1:]
	j := 0
	for j < len(buf) && buf[j] != ' ' {
		j++
	}
	return string(buf[:j])
}

func init() {
	ConfigureFromEnv()
}

// ConfigureFromEnv reloads LOG_LEVEL, LOG_PATH, and GENYEE_LOG_FORMAT from the
// process environment.
func ConfigureFromEnv() {
	loggerMu.Lock()
	defer loggerMu.Unlock()

	if activeLogFile != nil {
		if err := activeLogFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "logger: failed to close previous log file: %v\n", err)
		}
		activeLogFile = nil
	}

	logLevel := getLogLevelFromEnv()
	appLogger.SetLevel(logLevel)

	writer := io.Writer(os.Stdout)
	logPath := resolveLogPathFromEnv()
	if logPath != "" {
		file, err := openLogFile(logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "logger: failed to open log file %s: %v\n", logPath, err)
		} else {
			activeLogFile = file
			writer = io.MultiWriter(os.Stdout, &ansiStripWriter{w: file})
		}
	}

	appLogger.SetOutput(writer)

	forceColor := false
	if fi, err := os.Stdout.Stat(); err == nil {
		forceColor = (fi.Mode() & os.ModeCharDevice) != 0
	}

	tmpl := resolveLogFormatFromEnv()
	appLogger.SetFormatter(&CustomFormatter{
		ForceColor:   forceColor,
		Template:     tmpl,
		threadNeeded: strings.Contains(tmpl, "%thread"),
	})
	appLogger.SetReportCaller(false)
}

// GetLogger retrieves the context-bound logrus Entry, or returns a fresh entry
// backed by appLogger when c is nil or carries no logger.
func GetLogger(c context.Context) *logrus.Entry {
	if c != nil {
		if entry, ok := c.Value(types.LoggerContextKey).(*logrus.Entry); ok && entry != nil {
			return entry
		}
	}
	return logrus.NewEntry(appLogger)
}

// SetOutput overrides the internal logger's output destination.
func SetOutput(w io.Writer) {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	appLogger.SetOutput(w)
}

// SetLogLevel updates the global application logger's minimum severity level.
func SetLogLevel(level LogLevel) {
	var logLevel logrus.Level

	switch level {
	case LevelDebug:
		logLevel = logrus.DebugLevel
	case LevelInfo:
		logLevel = logrus.InfoLevel
	case LevelWarn:
		logLevel = logrus.WarnLevel
	case LevelError:
		logLevel = logrus.ErrorLevel
	case LevelFatal:
		logLevel = logrus.FatalLevel
	default:
		logLevel = logrus.InfoLevel
	}

	loggerMu.Lock()
	defer loggerMu.Unlock()
	appLogger.SetLevel(logLevel)
}

func getLogLevelFromEnv() logrus.Level {
	logLevelStr := strings.ToLower(os.Getenv("LOG_LEVEL"))

	switch logLevelStr {
	case "debug":
		return logrus.DebugLevel
	case "info":
		return logrus.InfoLevel
	case "warn", "warning":
		return logrus.WarnLevel
	case "error":
		return logrus.ErrorLevel
	case "fatal":
		return logrus.FatalLevel
	default:
		return logrus.DebugLevel
	}
}

func resolveLogPathFromEnv() string {
	if logPath := strings.TrimSpace(os.Getenv("LOG_PATH")); logPath != "" {
		return filepath.Clean(logPath)
	}
	return defaultMacAppLogPath()
}

func resolveLogFormatFromEnv() string {
	name := "GENYEE_LOG_FORMAT"
	raw, ok := os.LookupEnv(name)
	if !ok {
		name = "LOG_FORMAT"
		raw = os.Getenv(name)
	}
	tmpl := strings.TrimSpace(raw)
	if tmpl == "" {
		return ""
	}
	if !strings.Contains(tmpl, "%msg") {
		fmt.Fprintf(os.Stderr, "logger: %s must contain %%msg; using the default log format\n", name)
		return ""
	}
	if name == "LOG_FORMAT" {
		fmt.Fprintln(os.Stderr, "logger: LOG_FORMAT is deprecated; use GENYEE_LOG_FORMAT instead")
	}
	return tmpl
}

func defaultMacAppLogPath() string {
	execPath, err := os.Executable()
	if err != nil || !strings.Contains(execPath, ".app/Contents/MacOS") {
		return ""
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	appName := "GENYEE Lite"
	if idx := strings.Index(execPath, ".app/Contents/MacOS"); idx >= 0 {
		bundleName := filepath.Base(execPath[:idx+4])
		if trimmed := strings.TrimSuffix(bundleName, ".app"); trimmed != "" {
			appName = trimmed
		}
	}

	return filepath.Join(homeDir, "Library", "Logs", appName, appName+".log")
}

func openLogFile(logPath string) (io.WriteCloser, error) {
	dir := filepath.Dir(logPath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    50, // megabytes
		MaxBackups: 3,
		MaxAge:     28, // days
		Compress:   true,
	}, nil
}

func addCaller(entry *logrus.Entry, skip int) *logrus.Entry {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return entry
	}
	shortFile := path.Base(file)
	funcName := "unknown"
	if fn := runtime.FuncForPC(pc); fn != nil {
		fullName := path.Base(fn.Name())
		parts := strings.Split(fullName, ".")
		funcName = parts[len(parts)-1]
	}
	return entry.WithField("caller", fmt.Sprintf("%s:%d[%s]", shortFile, line, funcName))
}

// WithRequestID attaches request_id to the context logger.
func WithRequestID(c context.Context, requestID string) context.Context {
	return WithField(c, "request_id", requestID)
}

// WithField attaches a single structured field to the context logger.
func WithField(c context.Context, key string, value interface{}) context.Context {
	if c == nil {
		c = context.Background()
	}
	logger := GetLogger(c).WithField(key, value)
	return context.WithValue(c, types.LoggerContextKey, logger)
}

// WithFields attaches multiple structured fields to the context logger.
func WithFields(c context.Context, fields logrus.Fields) context.Context {
	if c == nil {
		c = context.Background()
	}
	logger := GetLogger(c).WithFields(fields)
	return context.WithValue(c, types.LoggerContextKey, logger)
}

// Debug logs args at debug severity.
func Debug(c context.Context, args ...interface{}) {
	addCaller(GetLogger(c), 2).Debug(args...)
}

// Debugf logs a formatted string at debug severity.
func Debugf(c context.Context, format string, args ...interface{}) {
	addCaller(GetLogger(c), 2).Debugf(format, args...)
}

// Info logs args at info severity.
func Info(c context.Context, args ...interface{}) {
	addCaller(GetLogger(c), 2).Info(args...)
}

// Infof logs a formatted string at info severity.
func Infof(c context.Context, format string, args ...interface{}) {
	addCaller(GetLogger(c), 2).Infof(format, args...)
}

// Warn logs args at warning severity.
func Warn(c context.Context, args ...interface{}) {
	addCaller(GetLogger(c), 2).Warn(args...)
}

// Warnf logs a formatted string at warning severity.
func Warnf(c context.Context, format string, args ...interface{}) {
	addCaller(GetLogger(c), 2).Warnf(format, args...)
}

// Fields aliases logrus.Fields so callers in other packages can use the
// short form `logger.Fields{...}` without importing logrus directly.
type Fields = logrus.Fields

// WarnWithFields emits a warning with structured fields.
func WarnWithFields(c context.Context, fields Fields, msg string) {
	if fields == nil {
		fields = Fields{}
	}
	addCaller(GetLogger(c), 2).WithFields(fields).Warn(msg)
}

// Error logs args at error severity.
func Error(c context.Context, args ...interface{}) {
	addCaller(GetLogger(c), 2).Error(args...)
}

// Errorf logs a formatted string at error severity.
func Errorf(c context.Context, format string, args ...interface{}) {
	addCaller(GetLogger(c), 2).Errorf(format, args...)
}

// ErrorWithFields logs an error with cloned structured fields.
func ErrorWithFields(c context.Context, err error, fields logrus.Fields) {
	cloned := make(logrus.Fields, len(fields)+1)
	for k, v := range fields {
		cloned[k] = v
	}
	if err != nil {
		cloned["error"] = err.Error()
	}
	addCaller(GetLogger(c), 2).WithFields(cloned).Error("发生错误")
}

// Fatal logs args at fatal severity and terminates the process.
func Fatal(c context.Context, args ...interface{}) {
	addCaller(GetLogger(c), 2).Fatal(args...)
}

// Fatalf logs a formatted string at fatal severity and terminates the process.
func Fatalf(c context.Context, format string, args ...interface{}) {
	addCaller(GetLogger(c), 2).Fatalf(format, args...)
}

// CloneContext copies detach-safe identity and tracing keys into a fresh
// background context so detached work preserves tenant/principal/trace identity
// without inheriting request cancellation.
func CloneContext(ctx context.Context) context.Context {
	newCtx := context.Background()
	if ctx == nil {
		return newCtx
	}

	for _, k := range types.ContextKeysClonedAcrossDetach() {
		if v := ctx.Value(k); v != nil {
			newCtx = context.WithValue(newCtx, k, v)
		}
	}

	if sp := trace.SpanFromContext(ctx); sp.IsRecording() {
		newCtx = trace.ContextWithSpan(newCtx, sp)
	}

	return newCtx
}

// CloneContextWithoutTrace copies the same identity keys as CloneContext but
// drops the Langfuse *Trace handle and the OpenTelemetry span.
func CloneContextWithoutTrace(ctx context.Context) context.Context {
	newCtx := context.Background()
	if ctx == nil {
		return newCtx
	}
	for _, k := range types.ContextKeysClonedAcrossDetach() {
		if k == types.LangfuseTraceContextKey {
			continue
		}
		if v := ctx.Value(k); v != nil {
			newCtx = context.WithValue(newCtx, k, v)
		}
	}
	return newCtx
}

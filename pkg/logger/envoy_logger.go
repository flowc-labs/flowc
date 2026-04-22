package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"
)

// EnvoyLogger implements the log.Logger interface from Envoy Go control plane
type EnvoyLogger struct {
	logger   *slog.Logger
	level    Level
	levelVar *slog.LevelVar // For dynamic level changes
}

// Level represents the logging level
type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
	FatalLevel
)

// String returns the string representation of the level
func (l Level) String() string {
	switch l {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	case FatalLevel:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// ToSlogLevel converts custom Level to slog.Level
func (l Level) ToSlogLevel() slog.Level {
	switch l {
	case DebugLevel:
		return slog.LevelDebug
	case InfoLevel:
		return slog.LevelInfo
	case WarnLevel:
		return slog.LevelWarn
	case ErrorLevel:
		return slog.LevelError
	case FatalLevel:
		return slog.LevelError // slog doesn't have fatal, use error
	default:
		return slog.LevelInfo
	}
}

// NewEnvoyLogger creates a new Envoy logger
func NewEnvoyLogger(level Level) *EnvoyLogger {
	// Create a level var for dynamic level changes
	levelVar := new(slog.LevelVar)
	levelVar.Set(level.ToSlogLevel())

	// Create a structured logger with JSON output
	opts := &slog.HandlerOptions{
		Level:     levelVar,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize the source attribute to show file:line
			if a.Key == slog.SourceKey {
				if source, ok := a.Value.Any().(*slog.Source); ok {
					// Extract just the filename from the full path
					parts := strings.Split(source.File, "/")
					filename := parts[len(parts)-1]
					return slog.String("source", fmt.Sprintf("%s:%d", filename, source.Line))
				}
			}
			return a
		},
	}

	handler := slog.NewJSONHandler(os.Stdout, opts)
	logger := slog.New(handler)

	return &EnvoyLogger{
		logger:   logger,
		level:    level,
		levelVar: levelVar,
	}
}

// NewEnvoyLoggerWithHandler creates a new Envoy logger with a custom handler
func NewEnvoyLoggerWithHandler(handler slog.Handler) *EnvoyLogger {
	logger := slog.New(handler)
	// Try to extract level var if possible
	levelVar := new(slog.LevelVar)
	levelVar.Set(slog.LevelInfo)
	return &EnvoyLogger{
		logger:   logger,
		level:    InfoLevel, // Default level
		levelVar: levelVar,
	}
}

// NewJSONLoggerWithWriter creates a JSON logger that writes to a specific writer
func NewJSONLoggerWithWriter(w io.Writer, level Level) *EnvoyLogger {
	// Create a level var for dynamic level changes
	levelVar := new(slog.LevelVar)
	levelVar.Set(level.ToSlogLevel())

	// Create a structured logger with JSON output
	opts := &slog.HandlerOptions{
		Level:     levelVar,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize the source attribute to show file:line
			if a.Key == slog.SourceKey {
				if source, ok := a.Value.Any().(*slog.Source); ok {
					// Extract just the filename from the full path
					parts := strings.Split(source.File, "/")
					filename := parts[len(parts)-1]
					return slog.String("source", fmt.Sprintf("%s:%d", filename, source.Line))
				}
			}
			return a
		},
	}

	handler := slog.NewJSONHandler(w, opts)
	logger := slog.New(handler)

	return &EnvoyLogger{
		logger:   logger,
		level:    level,
		levelVar: levelVar,
	}
}

// Debug logs a debug message
func (l *EnvoyLogger) Debug(msg string) {
	l.logWithSource(context.Background(), slog.LevelDebug, msg)
}

// Debugf logs a debug message with formatting
func (l *EnvoyLogger) Debugf(format string, args ...any) {
	l.logWithSource(context.Background(), slog.LevelDebug, fmt.Sprintf(format, args...))
}

// Info logs an info message
func (l *EnvoyLogger) Info(msg string) {
	l.logWithSource(context.Background(), slog.LevelInfo, msg)
}

// Infof logs an info message with formatting
func (l *EnvoyLogger) Infof(format string, args ...any) {
	l.logWithSource(context.Background(), slog.LevelInfo, fmt.Sprintf(format, args...))
}

// Warn logs a warning message
func (l *EnvoyLogger) Warn(msg string) {
	l.logWithSource(context.Background(), slog.LevelWarn, msg)
}

// Warnf logs a warning message with formatting
func (l *EnvoyLogger) Warnf(format string, args ...any) {
	l.logWithSource(context.Background(), slog.LevelWarn, fmt.Sprintf(format, args...))
}

// Error logs an error message
func (l *EnvoyLogger) Error(msg string) {
	l.logWithSource(context.Background(), slog.LevelError, msg)
}

// Errorf logs an error message with formatting
func (l *EnvoyLogger) Errorf(format string, args ...any) {
	l.logWithSource(context.Background(), slog.LevelError, fmt.Sprintf(format, args...))
}

// Fatal logs a fatal message and exits
func (l *EnvoyLogger) Fatal(msg string) {
	l.logWithSource(context.Background(), slog.LevelError, msg, "level", "FATAL")
	os.Exit(1)
}

// Fatalf logs a fatal message with formatting and exits
func (l *EnvoyLogger) Fatalf(format string, args ...any) {
	l.logWithSource(context.Background(), slog.LevelError, fmt.Sprintf(format, args...), "level", "FATAL")
	os.Exit(1)
}

// logWithSource logs a message with the correct source location
func (l *EnvoyLogger) logWithSource(ctx context.Context, level slog.Level, msg string, args ...any) {
	if !l.logger.Enabled(ctx, level) {
		return
	}

	// Callers(3): skip runtime.Callers, this frame, and the public logger method.
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])

	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	for i := 0; i < len(args); i += 2 {
		if i+1 < len(args) {
			r.Add(args[i].(string), args[i+1])
		}
	}

	_ = l.logger.Handler().Handle(ctx, r)
}

// WithField adds a field to the logger
func (l *EnvoyLogger) WithField(key string, value any) *EnvoyLogger {
	return &EnvoyLogger{
		logger:   l.logger.With(key, value),
		level:    l.level,
		levelVar: l.levelVar,
	}
}

// WithFields adds multiple fields to the logger
func (l *EnvoyLogger) WithFields(fields map[string]any) *EnvoyLogger {
	args := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return &EnvoyLogger{
		logger:   l.logger.With(args...),
		level:    l.level,
		levelVar: l.levelVar,
	}
}

// WithError adds an error field to the logger
func (l *EnvoyLogger) WithError(err error) *EnvoyLogger {
	return l.WithField("error", err.Error())
}

// WithContext adds context to the logger
// Note: This is a basic implementation. Extend it to extract trace IDs,
// request IDs, or other context values as needed.
func (l *EnvoyLogger) WithContext(ctx context.Context) *EnvoyLogger {
	// For now, just return the same logger
	// In a more sophisticated implementation, you might extract values from context
	return l
}

// SetLevel sets the logging level dynamically
func (l *EnvoyLogger) SetLevel(level Level) {
	l.level = level
	if l.levelVar != nil {
		l.levelVar.Set(level.ToSlogLevel())
	}
}

// GetLevel returns the current logging level
func (l *EnvoyLogger) GetLevel() Level {
	return l.level
}

// IsDebugEnabled returns true if debug logging is enabled
func (l *EnvoyLogger) IsDebugEnabled() bool {
	return l.level <= DebugLevel
}

// IsInfoEnabled returns true if info logging is enabled
func (l *EnvoyLogger) IsInfoEnabled() bool {
	return l.level <= InfoLevel
}

// IsWarnEnabled returns true if warning logging is enabled
func (l *EnvoyLogger) IsWarnEnabled() bool {
	return l.level <= WarnLevel
}

// IsErrorEnabled returns true if error logging is enabled
func (l *EnvoyLogger) IsErrorEnabled() bool {
	return l.level <= ErrorLevel
}

// NewDefaultEnvoyLogger creates a default Envoy logger with INFO level
func NewDefaultEnvoyLogger() *EnvoyLogger {
	return NewEnvoyLogger(InfoLevel)
}

// NewDebugEnvoyLogger creates a debug Envoy logger
func NewDebugEnvoyLogger() *EnvoyLogger {
	return NewEnvoyLogger(DebugLevel)
}

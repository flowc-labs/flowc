package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLoggerLevels(t *testing.T) {
	tests := []struct {
		name     string
		level    Level
		logFunc  func(*EnvoyLogger)
		expected bool // should log or not
	}{
		{
			name:     "Debug level logs debug",
			level:    DebugLevel,
			logFunc:  func(l *EnvoyLogger) { l.Debug("test") },
			expected: true,
		},
		{
			name:     "Info level filters debug",
			level:    InfoLevel,
			logFunc:  func(l *EnvoyLogger) { l.Debug("test") },
			expected: false,
		},
		{
			name:     "Info level logs info",
			level:    InfoLevel,
			logFunc:  func(l *EnvoyLogger) { l.Info("test") },
			expected: true,
		},
		{
			name:     "Warn level filters info",
			level:    WarnLevel,
			logFunc:  func(l *EnvoyLogger) { l.Info("test") },
			expected: false,
		},
		{
			name:     "Warn level logs warn",
			level:    WarnLevel,
			logFunc:  func(l *EnvoyLogger) { l.Warn("test") },
			expected: true,
		},
		{
			name:     "Error level filters warn",
			level:    ErrorLevel,
			logFunc:  func(l *EnvoyLogger) { l.Warn("test") },
			expected: false,
		},
		{
			name:     "Error level logs error",
			level:    ErrorLevel,
			logFunc:  func(l *EnvoyLogger) { l.Error("test") },
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := NewTextEnvoyLoggerWithWriter(&buf, tt.level)
			tt.logFunc(log.EnvoyLogger)

			output := buf.String()
			if tt.expected && output == "" {
				t.Errorf("Expected log output, got none")
			}
			if !tt.expected && output != "" {
				t.Errorf("Expected no log output, got: %s", output)
			}
		})
	}
}

func TestLoggerFormattedMethods(t *testing.T) {
	tests := []struct {
		name     string
		logFunc  func(*EnvoyLogger)
		contains string
	}{
		{
			name:     "Debugf formats correctly",
			logFunc:  func(l *EnvoyLogger) { l.Debugf("count: %d", 42) },
			contains: "count: 42",
		},
		{
			name:     "Infof formats correctly",
			logFunc:  func(l *EnvoyLogger) { l.Infof("user: %s", "john") },
			contains: "user: john",
		},
		{
			name:     "Warnf formats correctly",
			logFunc:  func(l *EnvoyLogger) { l.Warnf("warning: %v", true) },
			contains: "warning: true",
		},
		{
			name:     "Errorf formats correctly",
			logFunc:  func(l *EnvoyLogger) { l.Errorf("error: %s", "failed") },
			contains: "error: failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := NewTextEnvoyLoggerWithWriter(&buf, DebugLevel)
			tt.logFunc(log.EnvoyLogger)

			output := buf.String()
			if !strings.Contains(output, tt.contains) {
				t.Errorf("Expected output to contain %q, got: %s", tt.contains, output)
			}
		})
	}
}

func TestJSONLogger(t *testing.T) {
	var buf bytes.Buffer

	// Create a JSON logger that writes to buffer
	config := &LoggerConfig{
		Type:   JSONLogger,
		Level:  InfoLevel,
		Output: &buf,
	}
	log := NewLogger(config)

	log.Info("test message")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		t.Fatal("No log output")
	}

	// Parse JSON to verify structure
	var logEntry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON log output: %v\nOutput: %q", err, lines[0])
	}

	// Verify required fields
	if msg, ok := logEntry["msg"].(string); !ok || msg != "test message" {
		t.Errorf("Expected msg='test message', got: %v", logEntry["msg"])
	}

	if level, ok := logEntry["level"].(string); !ok || level != "INFO" {
		t.Errorf("Expected level='INFO', got: %v", logEntry["level"])
	}

	if _, ok := logEntry["time"]; !ok {
		t.Error("Expected 'time' field in JSON output")
	}

	if _, ok := logEntry["source"]; !ok {
		t.Error("Expected 'source' field in JSON output")
	}
}

func TestTextLogger(t *testing.T) {
	var buf bytes.Buffer
	log := NewTextEnvoyLoggerWithWriter(&buf, InfoLevel)

	log.Info("test message")

	output := buf.String()

	// Verify output contains expected components
	if !strings.Contains(output, "INFO") {
		t.Error("Expected output to contain 'INFO'")
	}
	if !strings.Contains(output, "test message") {
		t.Error("Expected output to contain 'test message'")
	}
	if !strings.Contains(output, "time=") {
		t.Error("Expected output to contain 'time='")
	}
}

func TestWithField(t *testing.T) {
	var buf bytes.Buffer

	config := &LoggerConfig{
		Type:   JSONLogger,
		Level:  InfoLevel,
		Output: &buf,
	}
	log := NewLogger(config)

	log.WithField("user_id", "123").Info("user action")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		t.Fatal("No log output")
	}

	var logEntry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if userID, ok := logEntry["user_id"].(string); !ok || userID != "123" {
		t.Errorf("Expected user_id='123', got: %v", logEntry["user_id"])
	}
}

func TestWithFields(t *testing.T) {
	var buf bytes.Buffer

	config := &LoggerConfig{
		Type:   JSONLogger,
		Level:  InfoLevel,
		Output: &buf,
	}
	log := NewLogger(config)

	fields := map[string]any{
		"user_id":    "123",
		"request_id": "abc",
		"count":      42,
	}

	log.WithFields(fields).Info("multi-field log")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		t.Fatal("No log output")
	}

	var logEntry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if userID, ok := logEntry["user_id"].(string); !ok || userID != "123" {
		t.Errorf("Expected user_id='123', got: %v", logEntry["user_id"])
	}

	if requestID, ok := logEntry["request_id"].(string); !ok || requestID != "abc" {
		t.Errorf("Expected request_id='abc', got: %v", logEntry["request_id"])
	}

	if count, ok := logEntry["count"].(float64); !ok || count != 42 {
		t.Errorf("Expected count=42, got: %v", logEntry["count"])
	}
}

func TestWithError(t *testing.T) {
	var buf bytes.Buffer

	config := &LoggerConfig{
		Type:   JSONLogger,
		Level:  InfoLevel,
		Output: &buf,
	}
	log := NewLogger(config)

	testErr := errors.New("test error")
	log.WithError(testErr).Error("operation failed")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		t.Fatal("No log output")
	}

	var logEntry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if errMsg, ok := logEntry["error"].(string); !ok || errMsg != "test error" {
		t.Errorf("Expected error='test error', got: %v", logEntry["error"])
	}
}

func TestDynamicLevelChange(t *testing.T) {
	var buf bytes.Buffer
	log := NewTextEnvoyLoggerWithWriter(&buf, InfoLevel)

	// Debug should be filtered at INFO level
	log.Debug("should not appear")
	if buf.String() != "" {
		t.Error("Expected debug message to be filtered at INFO level")
	}

	// Change to DEBUG level
	log.SetLevel(DebugLevel)
	buf.Reset()

	// Debug should now appear
	log.Debug("should appear")
	if buf.String() == "" {
		t.Error("Expected debug message to appear at DEBUG level")
	}

	// Change back to INFO level
	log.SetLevel(InfoLevel)
	buf.Reset()

	// Debug should be filtered again
	log.Debug("should not appear again")
	if buf.String() != "" {
		t.Error("Expected debug message to be filtered after changing back to INFO level")
	}
}

func TestIsLevelEnabled(t *testing.T) {
	tests := []struct {
		name     string
		level    Level
		checkFn  func(*EnvoyLogger) bool
		expected bool
	}{
		{"Debug enabled at Debug", DebugLevel, (*EnvoyLogger).IsDebugEnabled, true},
		{"Debug disabled at Info", InfoLevel, (*EnvoyLogger).IsDebugEnabled, false},
		{"Info enabled at Debug", DebugLevel, (*EnvoyLogger).IsInfoEnabled, true},
		{"Info enabled at Info", InfoLevel, (*EnvoyLogger).IsInfoEnabled, true},
		{"Info disabled at Warn", WarnLevel, (*EnvoyLogger).IsInfoEnabled, false},
		{"Warn enabled at Info", InfoLevel, (*EnvoyLogger).IsWarnEnabled, true},
		{"Error enabled at Warn", WarnLevel, (*EnvoyLogger).IsErrorEnabled, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := NewEnvoyLogger(tt.level)
			result := tt.checkFn(log)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestGetSetLevel(t *testing.T) {
	log := NewEnvoyLogger(InfoLevel)

	if log.GetLevel() != InfoLevel {
		t.Errorf("Expected initial level to be InfoLevel, got %v", log.GetLevel())
	}

	log.SetLevel(DebugLevel)
	if log.GetLevel() != DebugLevel {
		t.Errorf("Expected level to be DebugLevel after SetLevel, got %v", log.GetLevel())
	}
}

func TestLevelString(t *testing.T) {
	tests := []struct {
		level    Level
		expected string
	}{
		{DebugLevel, "DEBUG"},
		{InfoLevel, "INFO"},
		{WarnLevel, "WARN"},
		{ErrorLevel, "ERROR"},
		{FatalLevel, "FATAL"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.level.String() != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, tt.level.String())
			}
		})
	}
}

func TestLevelToSlogLevel(t *testing.T) {
	tests := []struct {
		level    Level
		expected string
	}{
		{DebugLevel, "DEBUG"},
		{InfoLevel, "INFO"},
		{WarnLevel, "WARN"},
		{ErrorLevel, "ERROR"},
		{FatalLevel, "ERROR"}, // Fatal maps to Error in slog
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			slogLevel := tt.level.ToSlogLevel()
			// Verify it's a valid slog level by checking its string representation
			if slogLevel.String() != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, slogLevel.String())
			}
		})
	}
}

func TestSourceLocation(t *testing.T) {
	var buf bytes.Buffer

	config := &LoggerConfig{
		Type:   JSONLogger,
		Level:  InfoLevel,
		Output: &buf,
	}
	log := NewLogger(config)

	log.Info("test") // This is line 392

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		t.Fatal("No log output")
	}

	var logEntry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	source, ok := logEntry["source"].(string)
	if !ok {
		t.Fatal("Expected 'source' field in log output")
	}

	// Verify source contains the test file name
	if !strings.Contains(source, "logger_test.go") {
		t.Errorf("Expected source to contain 'logger_test.go', got: %s", source)
	}

	// Verify source is not from the logger wrapper
	if strings.Contains(source, "envoy_logger.go") {
		t.Errorf("Source should not be from envoy_logger.go, got: %s", source)
	}
}

func TestDefaultLoggers(t *testing.T) {
	t.Run("NewDefaultEnvoyLogger", func(t *testing.T) {
		log := NewDefaultEnvoyLogger()
		if log.GetLevel() != InfoLevel {
			t.Errorf("Expected default level to be InfoLevel, got %v", log.GetLevel())
		}
	})

	t.Run("NewDebugEnvoyLogger", func(t *testing.T) {
		log := NewDebugEnvoyLogger()
		if log.GetLevel() != DebugLevel {
			t.Errorf("Expected debug logger level to be DebugLevel, got %v", log.GetLevel())
		}
	})

	t.Run("NewDefaultTextEnvoyLogger", func(t *testing.T) {
		log := NewDefaultTextEnvoyLogger()
		if log.GetLevel() != InfoLevel {
			t.Errorf("Expected default text logger level to be InfoLevel, got %v", log.GetLevel())
		}
	})
}

func TestLoggerFactory(t *testing.T) {
	t.Run("NewJSONLogger", func(t *testing.T) {
		log := NewJSONLogger(DebugLevel)
		if log.GetLevel() != DebugLevel {
			t.Errorf("Expected level to be DebugLevel, got %v", log.GetLevel())
		}
	})

	t.Run("NewTextLogger", func(t *testing.T) {
		log := NewTextLogger(InfoLevel)
		if log.GetLevel() != InfoLevel {
			t.Errorf("Expected level to be InfoLevel, got %v", log.GetLevel())
		}
	})

	t.Run("DefaultLoggerConfig", func(t *testing.T) {
		config := DefaultLoggerConfig()
		if config.Type != TextLogger {
			t.Errorf("Expected default type to be TextLogger, got %v", config.Type)
		}
		if config.Level != InfoLevel {
			t.Errorf("Expected default level to be InfoLevel, got %v", config.Level)
		}
		if !config.AddSource {
			t.Error("Expected AddSource to be true by default")
		}
	})
}

func BenchmarkBasicLogging(b *testing.B) {
	var buf bytes.Buffer
	log := NewTextEnvoyLoggerWithWriter(&buf, InfoLevel)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		log.Info("benchmark message")
	}
}

func BenchmarkStructuredLogging(b *testing.B) {
	var buf bytes.Buffer
	log := NewTextEnvoyLoggerWithWriter(&buf, InfoLevel)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		log.EnvoyLogger.WithFields(map[string]any{
			"user_id":    "123",
			"request_id": "abc",
			"count":      42,
		}).Info("benchmark message")
	}
}

func BenchmarkFilteredLogging(b *testing.B) {
	var buf bytes.Buffer
	log := NewTextEnvoyLoggerWithWriter(&buf, InfoLevel)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// This should be filtered out
		log.Debug("benchmark message")
	}
}

func BenchmarkJSONLogging(b *testing.B) {
	var buf bytes.Buffer
	config := &LoggerConfig{
		Type:   JSONLogger,
		Level:  InfoLevel,
		Output: &buf,
	}
	log := NewLogger(config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		log.Info("benchmark message")
	}
}

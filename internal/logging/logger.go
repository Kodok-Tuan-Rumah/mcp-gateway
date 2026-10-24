// Package logging provides structured JSON logging utilities for Go services
//
// Usage:
//   logger := logging.NewLogger("mcp-gateway", logging.INFO)
//   logger.Info("Processing request", logging.Fields{
//       "request_id": "123",
//       "backend": "research_rag",
//   })
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// LogLevel represents the severity level
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARNING
	ERROR
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARNING:
		return "WARNING"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Fields is a map of additional log fields
type Fields map[string]interface{}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp  string                 `json:"timestamp"`
	Level      string                 `json:"level"`
	Service    string                 `json:"service"`
	Message    string                 `json:"message"`
	RequestID  string                 `json:"request_id,omitempty"`
	DurationMs float64                `json:"duration_ms,omitempty"`
	Backend    string                 `json:"backend,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Extra      map[string]interface{} `json:"extra,omitempty"`
}

// Logger is a structured JSON logger
type Logger struct {
	serviceName string
	level       LogLevel
	output      io.Writer
}

// NewLogger creates a new structured logger
func NewLogger(serviceName string, level LogLevel) *Logger {
	return &Logger{
		serviceName: serviceName,
		level:       level,
		output:      os.Stdout,
	}
}

// NewLoggerWithOutput creates a new logger with custom output
func NewLoggerWithOutput(serviceName string, level LogLevel, output io.Writer) *Logger {
	return &Logger{
		serviceName: serviceName,
		level:       level,
		output:      output,
	}
}

// SetLevel changes the log level
func (l *Logger) SetLevel(level LogLevel) {
	l.level = level
}

// SetOutput changes the output destination
func (l *Logger) SetOutput(output io.Writer) {
	l.output = output
}

// log is the internal logging method
func (l *Logger) log(level LogLevel, message string, fields Fields) {
	if level < l.level {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339) + "Z",
		Level:     level.String(),
		Service:   l.serviceName,
		Message:   message,
	}

	// Extract common fields
	if requestID, ok := fields["request_id"].(string); ok {
		entry.RequestID = requestID
		delete(fields, "request_id")
	}
	if durationMs, ok := fields["duration_ms"].(float64); ok {
		entry.DurationMs = durationMs
		delete(fields, "duration_ms")
	}
	if backend, ok := fields["backend"].(string); ok {
		entry.Backend = backend
		delete(fields, "backend")
	}
	if err, ok := fields["error"].(string); ok {
		entry.Error = err
		delete(fields, "error")
	}

	// Remaining fields go to Extra
	if len(fields) > 0 {
		entry.Extra = fields
	}

	jsonBytes, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(l.output, `{"timestamp":"%s","level":"ERROR","service":"%s","message":"Failed to marshal log entry: %v"}`,
			time.Now().UTC().Format(time.RFC3339)+"Z", l.serviceName, err)
		return
	}

	fmt.Fprintln(l.output, string(jsonBytes))
}

// Debug logs a DEBUG level message
func (l *Logger) Debug(message string, fields Fields) {
	l.log(DEBUG, message, fields)
}

// Info logs an INFO level message
func (l *Logger) Info(message string, fields Fields) {
	l.log(INFO, message, fields)
}

// Warning logs a WARNING level message
func (l *Logger) Warning(message string, fields Fields) {
	l.log(WARNING, message, fields)
}

// Error logs an ERROR level message
func (l *Logger) Error(message string, fields Fields) {
	l.log(ERROR, message, fields)
}

// WithFields creates a new log entry builder with fields
func (l *Logger) WithFields(fields Fields) *LogBuilder {
	return &LogBuilder{
		logger: l,
		fields: fields,
	}
}

// LogBuilder is a fluent builder for log entries
type LogBuilder struct {
	logger *Logger
	fields Fields
	level  LogLevel
}

// With adds a field to the log entry
func (b *LogBuilder) With(key string, value interface{}) *LogBuilder {
	b.fields[key] = value
	return b
}

// WithRequestID adds a request ID field
func (b *LogBuilder) WithRequestID(requestID string) *LogBuilder {
	b.fields["request_id"] = requestID
	return b
}

// WithDuration adds a duration field (in milliseconds)
func (b *LogBuilder) WithDuration(duration time.Duration) *LogBuilder {
	b.fields["duration_ms"] = float64(duration.Milliseconds())
	return b
}

// WithBackend adds a backend field
func (b *LogBuilder) WithBackend(backend string) *LogBuilder {
	b.fields["backend"] = backend
	return b
}

// WithError adds an error field
func (b *LogBuilder) WithError(err error) *LogBuilder {
	b.fields["error"] = err.Error()
	return b
}

// Debug logs at DEBUG level
func (b *LogBuilder) Debug(message string) {
	b.logger.log(DEBUG, message, b.fields)
}

// Info logs at INFO level
func (b *LogBuilder) Info(message string) {
	b.logger.log(INFO, message, b.fields)
}

// Warning logs at WARNING level
func (b *LogBuilder) Warning(message string) {
	b.logger.log(WARNING, message, b.fields)
}

// Error logs at ERROR level
func (b *LogBuilder) Error(message string) {
	b.logger.log(ERROR, message, b.fields)
}

// DurationLogger tracks operation duration
type DurationLogger struct {
	logger    *Logger
	startTime time.Time
	fields    Fields
	level     LogLevel
}

// StartDuration begins a duration measurement
func (l *Logger) StartDuration(operation string, fields Fields) *DurationLogger {
	return &DurationLogger{
		logger:    l,
		startTime: time.Now(),
		fields:    fields,
		level:     INFO,
	}
}

// StartDurationDebug begins a duration measurement at DEBUG level
func (l *Logger) StartDurationDebug(operation string, fields Fields) *DurationLogger {
	return &DurationLogger{
		logger:    l,
		startTime: time.Now(),
		fields:    fields,
		level:     DEBUG,
	}
}

// End logs the duration
func (d *DurationLogger) End(message string) {
	duration := time.Since(d.startTime)
	d.fields["duration_ms"] = float64(duration.Milliseconds())
	d.logger.log(d.level, message, d.fields)
}

// EndWithError logs the duration with an error
func (d *DurationLogger) EndWithError(message string, err error) {
	duration := time.Since(d.startTime)
	d.fields["duration_ms"] = float64(duration.Milliseconds())
	if err != nil {
		d.fields["error"] = err.Error()
		d.logger.log(ERROR, message, d.fields)
	} else {
		d.logger.log(d.level, message, d.fields)
	}
}

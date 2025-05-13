package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const logDir = "logs" // Added constant

// Logger defines the structure for our simplified logger.
type Logger struct {
	logger  *log.Logger // Single logger instance
	logFile *os.File
}

// NewLogger creates and returns a new Logger instance.
// It initializes loggers for different levels, writing to both stdout and a log file.
// Returns an error if log directory or file cannot be created/opened.
func NewLogger() (*Logger, error) { // Modified signature
	// Ensure log directory exists
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory '%s': %w", logDir, err)
	}

	// Generate log filename (RFC3339 with underscores instead of colons)
	logFilename := fmt.Sprintf("%s.log", strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339), ":", "_"))
	logFilePath := filepath.Join(logDir, logFilename)

	// Open/Create log file for appending
	logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file '%s': %w", logFilePath, err)
	}

	// Create a MultiWriter to write to both stdout and the file
	multiWriter := io.MultiWriter(os.Stdout, logFile)

	// Using standard log flags LstdFlags might include date/time,
	// but we will prepend our custom RFC3339 timestamp manually.
	// We remove default flags to avoid duplicate timestamps.
	flags := 0
	logger := &Logger{
		// Initialize the single logger without any prefix other than the timestamp
		logger:  log.New(multiWriter, "", flags),
		logFile: logFile,
	}
	return logger, nil
}

// Close closes the underlying log file. // Added method
// It should be called when the application exits to ensure logs are flushed.
func (l *Logger) Close() error {
	if l.logFile != nil {
		return l.logFile.Close()
	}
	return nil
}

// formatMessage prepends the RFC3339 timestamp to the message.
func formatMessage(message string) string {
	// Use UTC time for RFC3339 Z format
	return fmt.Sprintf("%s | %s", time.Now().UTC().Format(time.RFC3339), message)
}

// Log logs a message.
func (l *Logger) Log(message string) {
	l.logger.Println(formatMessage(message))
}

// Logf logs a formatted message.
func (l *Logger) Logf(format string, v ...interface{}) {
	l.logger.Println(formatMessage(fmt.Sprintf(format, v...)))
}

// PrintProgress prints a message to the logger's writer without a timestamp or newline,
// appending a carriage return to allow overwriting the current line.
// This is intended for progress indicators.
func (l *Logger) PrintProgress(message string) {
	// Directly write to the multiWriter via l.logger.Writer()
	// This bypasses the log.Logger's automatic newline and prefixing.
	_, err := io.WriteString(l.logger.Writer(), message+"\r")
	if err != nil {
		// Fallback or handle error, e.g., log it normally
		l.Logf("Error writing progress: %v", err)
	}
}

// ClearLine clears the current console line by printing spaces and a carriage return.
// It writes to the logger's writer.
func (l *Logger) ClearLine() {
	_, err := io.WriteString(l.logger.Writer(), strings.Repeat(" ", 100)+"\r")
	if err != nil {
		l.Logf("Error clearing line: %v", err)
	}
}

// PrintOverwritable prints a message that should overwrite the current line,
// similar to PrintProgress. It's typically used for a final status message
// that replaces the last progress update.
func (l *Logger) PrintOverwritable(message string) {
	_, err := io.WriteString(l.logger.Writer(), message+"\r")
	if err != nil {
		l.Logf("Error writing overwritable message: %v", err)
	}
}

// StandardLog behaves like Log but is explicitly named to differentiate
// from progress logging. It ensures timestamp and newline.
// This can be an alias or a new method if Log/Logf are kept as is.
// For now, Log and Logf serve this purpose.

// File logging is implemented via io.MultiWriter in NewLogger.
// Level-specific methods removed.

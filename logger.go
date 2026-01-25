package audit

import (
	"context"
	"fmt"
	"log"
	"time"
)

// Config holds configuration for the audit logger
type Config struct {
	// Enabled controls whether audit logging is enabled
	Enabled bool

	// MaskDestination controls whether destinations (phone/email) should be masked
	MaskDestination bool

	// TTL for Redis/cache storage (0 means use storage default)
	TTL time.Duration

	// Writer configuration (for async writing)
	Writer *WriterConfig
}

// DefaultConfig returns default audit configuration
func DefaultConfig() *Config {
	return &Config{
		Enabled:         true,
		MaskDestination: true,
		TTL:             7 * 24 * time.Hour, // 7 days
		Writer:          DefaultWriterConfig(),
	}
}

// Logger handles audit logging with optional async writing
type Logger struct {
	config      *Config
	storage     Storage
	writer      *Writer
	logCallback func(record *Record)
}

// NewLogger creates a new audit logger with storage
func NewLogger(storage Storage, config *Config) *Logger {
	if config == nil {
		config = DefaultConfig()
	}

	logger := &Logger{
		config:  config,
		storage: storage,
	}

	return logger
}

// NewLoggerWithWriter creates a new audit logger with async writer
func NewLoggerWithWriter(storage Storage, config *Config) *Logger {
	if config == nil {
		config = DefaultConfig()
	}

	writer := NewWriter(storage, config.Writer)
	writer.Start()

	return &Logger{
		config:  config,
		storage: storage,
		writer:  writer,
	}
}

// SetLogCallback sets a callback that is called for each log entry
// Useful for also logging to standard logger
func (l *Logger) SetLogCallback(fn func(record *Record)) {
	l.logCallback = fn
}

// Stop stops the logger and releases resources
func (l *Logger) Stop() error {
	if l.writer != nil {
		return l.writer.Stop()
	}
	if l.storage != nil {
		return l.storage.Close()
	}
	return nil
}

// Log records an audit event
func (l *Logger) Log(ctx context.Context, record *Record) {
	if !l.config.Enabled {
		return
	}

	// Set timestamp if not set
	if record.Timestamp == 0 {
		record.Timestamp = time.Now().Unix()
	}

	// Mask destination if configured
	if l.config.MaskDestination && record.Destination != "" {
		record.Destination = MaskDestination(record.Destination, record.Channel)
	}

	// Use async writer if available
	if l.writer != nil {
		l.writer.Enqueue(record)
	} else if l.storage != nil {
		// Sync write
		if err := l.storage.Write(ctx, record); err != nil {
			log.Printf("[audit] Failed to write audit record: %v", err)
		}
	}

	// Call log callback if set
	if l.logCallback != nil {
		l.logCallback(record)
	}
}

// LogChallenge logs a challenge-related event
func (l *Logger) LogChallenge(ctx context.Context, eventType EventType, challengeID, userID string, result Result, opts ...RecordOption) {
	record := NewRecord(eventType, result).
		WithChallengeID(challengeID).
		WithUserID(userID)

	for _, opt := range opts {
		opt(record)
	}

	l.Log(ctx, record)
}

// LogAuth logs an authentication event
func (l *Logger) LogAuth(ctx context.Context, eventType EventType, userID string, result Result, opts ...RecordOption) {
	record := NewRecord(eventType, result).
		WithUserID(userID)

	for _, opt := range opts {
		opt(record)
	}

	l.Log(ctx, record)
}

// LogAccess logs an access control event
func (l *Logger) LogAccess(ctx context.Context, eventType EventType, userID, resource string, result Result, opts ...RecordOption) {
	record := NewRecord(eventType, result).
		WithUserID(userID).
		WithResource(resource)

	for _, opt := range opts {
		opt(record)
	}

	l.Log(ctx, record)
}

// Query queries audit records from storage
func (l *Logger) Query(ctx context.Context, filter *QueryFilter) ([]*Record, error) {
	if l.storage == nil {
		return nil, fmt.Errorf("storage not configured")
	}
	return l.storage.Query(ctx, filter)
}

// GetStats returns writer statistics (if async writer is used)
func (l *Logger) GetStats() *Stats {
	if l.writer == nil {
		return nil
	}
	stats := l.writer.GetStats()
	return &stats
}

// RecordOption is a function that modifies a record
type RecordOption func(*Record)

// WithRecordIP returns an option that sets the IP
func WithRecordIP(ip string) RecordOption {
	return func(r *Record) {
		r.IP = ip
	}
}

// WithRecordUserAgent returns an option that sets the user agent
func WithRecordUserAgent(ua string) RecordOption {
	return func(r *Record) {
		r.UserAgent = ua
	}
}

// WithRecordChannel returns an option that sets the channel
func WithRecordChannel(channel string) RecordOption {
	return func(r *Record) {
		r.Channel = channel
	}
}

// WithRecordDestination returns an option that sets the destination
func WithRecordDestination(dest string) RecordOption {
	return func(r *Record) {
		r.Destination = dest
	}
}

// WithRecordPurpose returns an option that sets the purpose
func WithRecordPurpose(purpose string) RecordOption {
	return func(r *Record) {
		r.Purpose = purpose
	}
}

// WithRecordReason returns an option that sets the reason
func WithRecordReason(reason string) RecordOption {
	return func(r *Record) {
		r.Reason = reason
	}
}

// WithRecordProvider returns an option that sets the provider
func WithRecordProvider(provider, messageID string) RecordOption {
	return func(r *Record) {
		r.Provider = provider
		r.ProviderMessageID = messageID
	}
}

// WithRecordRequestID returns an option that sets the request ID
func WithRecordRequestID(requestID string) RecordOption {
	return func(r *Record) {
		r.RequestID = requestID
	}
}

// WithRecordTraceID returns an option that sets the trace ID
func WithRecordTraceID(traceID string) RecordOption {
	return func(r *Record) {
		r.TraceID = traceID
	}
}

// WithRecordMetadata returns an option that sets metadata
func WithRecordMetadata(key string, value interface{}) RecordOption {
	return func(r *Record) {
		if r.Metadata == nil {
			r.Metadata = make(map[string]interface{})
		}
		r.Metadata[key] = value
	}
}

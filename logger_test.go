package audit

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testStorage is a simple storage for testing logger
type testStorage struct {
	mu      sync.Mutex
	records []*Record
}

func newTestStorage() *testStorage {
	return &testStorage{
		records: make([]*Record, 0),
	}
}

func (s *testStorage) Write(ctx context.Context, record *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	return nil
}

func (s *testStorage) Query(ctx context.Context, filter *QueryFilter) ([]*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records, nil
}

func (s *testStorage) Close() error {
	return nil
}

func (s *testStorage) getRecords() []*Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*Record, len(s.records))
	copy(result, s.records)
	return result
}

func TestNewLogger(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	assert.NotNil(t, logger)
}

func TestNewLoggerWithWriter(t *testing.T) {
	store := newTestStorage()
	logger := NewLoggerWithWriter(store, nil)

	assert.NotNil(t, logger)

	stats := logger.GetStats()
	assert.NotNil(t, stats)
	assert.True(t, stats.Started)

	err := logger.Stop()
	assert.NoError(t, err)
}

func TestLogger_Log(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user123")

	logger.Log(context.Background(), record)

	records := store.getRecords()
	require.Len(t, records, 1)
	assert.Equal(t, "user123", records[0].UserID)
}

func TestLogger_Log_Disabled(t *testing.T) {
	store := newTestStorage()
	config := DefaultConfig()
	config.Enabled = false
	logger := NewLogger(store, config)

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	logger.Log(context.Background(), record)

	records := store.getRecords()
	assert.Len(t, records, 0)
}

func TestLogger_Log_MaskDestination(t *testing.T) {
	store := newTestStorage()
	config := DefaultConfig()
	config.MaskDestination = true
	logger := NewLogger(store, config)

	record := NewRecord(EventChallengeCreated, ResultSuccess).
		WithChannel("email").
		WithDestination("user@example.com")

	logger.Log(context.Background(), record)

	records := store.getRecords()
	require.Len(t, records, 1)
	assert.Equal(t, "u***@example.com", records[0].Destination)
}

func TestLogger_Log_NoMasking(t *testing.T) {
	store := newTestStorage()
	config := DefaultConfig()
	config.MaskDestination = false
	logger := NewLogger(store, config)

	record := NewRecord(EventChallengeCreated, ResultSuccess).
		WithChannel("email").
		WithDestination("user@example.com")

	logger.Log(context.Background(), record)

	records := store.getRecords()
	require.Len(t, records, 1)
	assert.Equal(t, "user@example.com", records[0].Destination)
}

func TestLogger_LogCallback(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	var callbackRecord *Record
	logger.SetLogCallback(func(record *Record) {
		callbackRecord = record
	})

	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user123")

	logger.Log(context.Background(), record)

	assert.NotNil(t, callbackRecord)
	assert.Equal(t, "user123", callbackRecord.UserID)
}

func TestLogger_LogChallenge(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	logger.LogChallenge(
		context.Background(),
		EventChallengeCreated,
		"ch_123",
		"user123",
		ResultSuccess,
		WithRecordChannel("email"),
		WithRecordDestination("test@example.com"),
		WithRecordIP("192.168.1.1"),
	)

	records := store.getRecords()
	require.Len(t, records, 1)
	assert.Equal(t, EventChallengeCreated, records[0].EventType)
	assert.Equal(t, "ch_123", records[0].ChallengeID)
	assert.Equal(t, "user123", records[0].UserID)
	assert.Equal(t, "email", records[0].Channel)
	// Destination should be masked
	assert.Contains(t, records[0].Destination, "***")
	assert.Equal(t, "192.168.1.1", records[0].IP)
}

func TestLogger_LogAuth(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	logger.LogAuth(
		context.Background(),
		EventLoginSuccess,
		"user123",
		ResultSuccess,
		WithRecordIP("192.168.1.1"),
		WithRecordUserAgent("Mozilla/5.0"),
	)

	records := store.getRecords()
	require.Len(t, records, 1)
	assert.Equal(t, EventLoginSuccess, records[0].EventType)
	assert.Equal(t, "user123", records[0].UserID)
	assert.Equal(t, "192.168.1.1", records[0].IP)
	assert.Equal(t, "Mozilla/5.0", records[0].UserAgent)
}

func TestLogger_LogAccess(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	logger.LogAccess(
		context.Background(),
		EventAccessGranted,
		"user123",
		"/api/users",
		ResultSuccess,
		WithRecordIP("192.168.1.1"),
	)

	records := store.getRecords()
	require.Len(t, records, 1)
	assert.Equal(t, EventAccessGranted, records[0].EventType)
	assert.Equal(t, "user123", records[0].UserID)
	assert.Equal(t, "/api/users", records[0].Resource)
}

func TestLogger_Query(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	// Add some records
	logger.Log(context.Background(), NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("user1"))
	logger.Log(context.Background(), NewRecord(EventLoginFailed, ResultFailure).WithUserID("user2"))

	results, err := logger.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestLogger_Query_NoStorage(t *testing.T) {
	logger := NewLogger(nil, nil)

	_, err := logger.Query(context.Background(), DefaultQueryFilter())
	assert.Error(t, err)
}

func TestLogger_WithAsyncWriter(t *testing.T) {
	store := newTestStorage()
	config := DefaultConfig()
	config.Writer = &WriterConfig{
		QueueSize: 10,
		Workers:   2,
	}
	logger := NewLoggerWithWriter(store, config)

	// Log multiple records
	for i := 0; i < 5; i++ {
		logger.Log(context.Background(), NewRecord(EventLoginSuccess, ResultSuccess))
	}

	// Wait for async processing
	time.Sleep(200 * time.Millisecond)

	err := logger.Stop()
	require.NoError(t, err)

	records := store.getRecords()
	assert.Len(t, records, 5)
}

func TestRecordOptions(t *testing.T) {
	record := NewRecord(EventLoginSuccess, ResultSuccess)

	options := []RecordOption{
		WithRecordIP("192.168.1.1"),
		WithRecordUserAgent("Mozilla/5.0"),
		WithRecordChannel("email"),
		WithRecordDestination("test@example.com"),
		WithRecordPurpose("login"),
		WithRecordReason("timeout"),
		WithRecordProvider("sendgrid", "msg_123"),
		WithRecordRequestID("req_123"),
		WithRecordTraceID("trace_abc"),
		WithRecordMetadata("key", "value"),
	}

	for _, opt := range options {
		opt(record)
	}

	assert.Equal(t, "192.168.1.1", record.IP)
	assert.Equal(t, "Mozilla/5.0", record.UserAgent)
	assert.Equal(t, "email", record.Channel)
	assert.Equal(t, "test@example.com", record.Destination)
	assert.Equal(t, "login", record.Purpose)
	assert.Equal(t, "timeout", record.Reason)
	assert.Equal(t, "sendgrid", record.Provider)
	assert.Equal(t, "msg_123", record.ProviderMessageID)
	assert.Equal(t, "req_123", record.RequestID)
	assert.Equal(t, "trace_abc", record.TraceID)
	assert.Equal(t, "value", record.Metadata["key"])
}

func TestLogger_Stop_WithStorage(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	// Stop should close storage
	err := logger.Stop()
	assert.NoError(t, err)
}

func TestLogger_Stop_NilStorageAndWriter(t *testing.T) {
	logger := &Logger{
		config:  DefaultConfig(),
		storage: nil,
		writer:  nil,
	}

	err := logger.Stop()
	assert.NoError(t, err)
}

func TestLogger_Log_SyncWrite(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	// Log without async writer (sync write)
	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user123")

	logger.Log(context.Background(), record)

	records := store.getRecords()
	require.Len(t, records, 1)
}

func TestLogger_Log_WithTimestampAlreadySet(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	ts := int64(1234567890)
	record := NewRecord(EventLoginSuccess, ResultSuccess).SetTimestamp(ts)

	logger.Log(context.Background(), record)

	records := store.getRecords()
	require.Len(t, records, 1)
	assert.Equal(t, ts, records[0].Timestamp)
}

func TestLogger_Log_EmptyDestinationNoMask(t *testing.T) {
	store := newTestStorage()
	config := DefaultConfig()
	config.MaskDestination = true
	logger := NewLogger(store, config)

	record := NewRecord(EventChallengeCreated, ResultSuccess).
		WithChannel("email").
		WithDestination("") // Empty destination

	logger.Log(context.Background(), record)

	records := store.getRecords()
	require.Len(t, records, 1)
	assert.Equal(t, "", records[0].Destination)
}

func TestLogger_GetStats_WithWriter(t *testing.T) {
	store := newTestStorage()
	logger := NewLoggerWithWriter(store, nil)
	defer func() { _ = logger.Stop() }()

	stats := logger.GetStats()
	assert.NotNil(t, stats)
	assert.True(t, stats.Started)
}

func TestLogger_GetStats_WithoutWriter(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	stats := logger.GetStats()
	assert.Nil(t, stats)
}

func TestLogger_Log_WithZeroTimestamp(t *testing.T) {
	store := newTestStorage()
	logger := NewLogger(store, nil)

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	record.Timestamp = 0 // Force zero timestamp

	logger.Log(context.Background(), record)

	records := store.getRecords()
	require.Len(t, records, 1)
	assert.Greater(t, records[0].Timestamp, int64(0)) // Should be auto-set
}

// errorTestStorage is a storage that fails on write
type errorTestStorage struct{}

func (s *errorTestStorage) Write(ctx context.Context, record *Record) error {
	return fmt.Errorf("write failed")
}

func (s *errorTestStorage) Query(ctx context.Context, filter *QueryFilter) ([]*Record, error) {
	return nil, nil
}

func (s *errorTestStorage) Close() error {
	return nil
}

func TestLogger_Log_WriteError(t *testing.T) {
	store := &errorTestStorage{}
	logger := NewLogger(store, nil)

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	// Should not panic on write error
	logger.Log(context.Background(), record)
}

func TestLogger_Log_MaskingWithDifferentChannels(t *testing.T) {
	store := newTestStorage()
	config := DefaultConfig()
	config.MaskDestination = true
	logger := NewLogger(store, config)

	// Test SMS channel masking
	record1 := NewRecord(EventSendSuccess, ResultSuccess).
		WithChannel("sms").
		WithDestination("13800138000")
	logger.Log(context.Background(), record1)

	// Test phone channel masking
	record2 := NewRecord(EventSendSuccess, ResultSuccess).
		WithChannel("phone").
		WithDestination("13800138000")
	logger.Log(context.Background(), record2)

	// Test unknown channel masking
	record3 := NewRecord(EventSendSuccess, ResultSuccess).
		WithChannel("unknown").
		WithDestination("some_data")
	logger.Log(context.Background(), record3)

	records := store.getRecords()
	require.Len(t, records, 3)

	// All destinations should be masked
	for _, r := range records {
		assert.NotEqual(t, "13800138000", r.Destination)
		assert.NotEqual(t, "some_data", r.Destination)
	}
}

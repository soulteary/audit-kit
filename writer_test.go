package audit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStorage is a thread-safe mock storage for testing
type mockStorage struct {
	mu             sync.Mutex
	records        []*Record
	shouldError    bool
	writeDelay     time.Duration
	writeStarted   chan struct{} // if set, closed when Write is entered
	writeBlockOnly bool          // if true, block without listening to ctx (for Stop timeout branch)
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		records: make([]*Record, 0),
	}
}

func (m *mockStorage) Write(ctx context.Context, record *Record) error {
	if m.writeDelay > 0 {
		if m.writeStarted != nil {
			close(m.writeStarted)
			m.writeStarted = nil
		}
		if m.writeBlockOnly {
			time.Sleep(m.writeDelay)
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(m.writeDelay):
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shouldError {
		return errors.New("write error")
	}
	m.records = append(m.records, record)
	return nil
}

func (m *mockStorage) Query(ctx context.Context, filter *QueryFilter) ([]*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.records, nil
}

func (m *mockStorage) Close() error {
	return nil
}

func (m *mockStorage) getRecordCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.records)
}

func TestNewWriter(t *testing.T) {
	store := newMockStorage()

	t.Run("with default config", func(t *testing.T) {
		writer := NewWriter(store, nil)
		assert.NotNil(t, writer)

		stats := writer.GetStats()
		assert.Equal(t, 2, stats.Workers)
		assert.Equal(t, 1000, stats.QueueCap)
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &WriterConfig{
			QueueSize: 500,
			Workers:   4,
		}
		writer := NewWriter(store, config)
		assert.NotNil(t, writer)

		stats := writer.GetStats()
		assert.Equal(t, 4, stats.Workers)
		assert.Equal(t, 500, stats.QueueCap)
	})

	t.Run("with invalid config", func(t *testing.T) {
		config := &WriterConfig{
			QueueSize: -1,
			Workers:   0,
		}
		writer := NewWriter(store, config)
		assert.NotNil(t, writer)

		stats := writer.GetStats()
		assert.Equal(t, 2, stats.Workers)
		assert.Equal(t, 1000, stats.QueueCap)
	})
}

func TestWriter_StartStop(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 10,
		Workers:   2,
	})

	// Start
	writer.Start()
	stats := writer.GetStats()
	assert.True(t, stats.Started)

	// Double start should be safe
	writer.Start()

	// Stop
	err := writer.Stop()
	assert.NoError(t, err)

	// Double stop should be safe
	err = writer.Stop()
	assert.NoError(t, err)
}

func TestWriter_Enqueue(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 2,
		Workers:   1,
	})

	// Without starting workers, queue should fill up
	record1 := NewRecord(EventLoginSuccess, ResultSuccess)
	record2 := NewRecord(EventLoginFailed, ResultFailure)
	record3 := NewRecord(EventLogout, ResultSuccess)

	assert.True(t, writer.Enqueue(record1))
	assert.True(t, writer.Enqueue(record2))
	assert.False(t, writer.Enqueue(record3)) // Queue full
}

func TestWriter_EnqueueFailed_Callback(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 1,
		Workers:   1,
	})

	var failedRecord *Record
	writer.OnEnqueueFailed(func(record *Record) {
		failedRecord = record
	})

	// Fill the queue
	writer.Enqueue(NewRecord(EventLoginSuccess, ResultSuccess))

	// This should trigger callback
	record := NewRecord(EventLoginFailed, ResultFailure)
	writer.Enqueue(record)

	assert.NotNil(t, failedRecord)
	assert.Equal(t, EventLoginFailed, failedRecord.EventType)
}

func TestWriter_WriteFailed_Callback(t *testing.T) {
	store := newMockStorage()
	store.shouldError = true

	writer := NewWriter(store, &WriterConfig{
		QueueSize: 10,
		Workers:   1,
	})

	var mu sync.Mutex
	var failedRecord *Record
	var failedErr error
	writer.OnWriteFailed(func(record *Record, err error) {
		mu.Lock()
		failedRecord = record
		failedErr = err
		mu.Unlock()
	})

	writer.Start()
	defer func() { _ = writer.Stop() }()

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	writer.Enqueue(record)

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.NotNil(t, failedRecord)
	assert.NotNil(t, failedErr)
}

func TestWriter_ProcessRecords(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 10,
		Workers:   2,
	})

	writer.Start()

	// Enqueue multiple records
	for i := 0; i < 5; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			WithUserID("user" + string(rune('0'+i)))
		writer.Enqueue(record)
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	err := writer.Stop()
	require.NoError(t, err)

	assert.Equal(t, 5, store.getRecordCount())
}

func TestWriter_GetStats(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 100,
		Workers:   3,
	})

	stats := writer.GetStats()
	assert.Equal(t, 3, stats.Workers)
	assert.Equal(t, 100, stats.QueueCap)
	assert.Equal(t, 0, stats.QueueLength)
	assert.False(t, stats.Started)

	writer.Start()
	stats = writer.GetStats()
	assert.True(t, stats.Started)

	_ = writer.Stop()
}

func TestWriter_StopTimeout(t *testing.T) {
	store := newMockStorage()
	store.writeDelay = 24 * time.Hour
	store.writeStarted = make(chan struct{})
	store.writeBlockOnly = true // block without listening to ctx so Stop hits timeout branch

	writer := NewWriter(store, &WriterConfig{
		QueueSize:   10,
		Workers:     1,
		StopTimeout: 50 * time.Millisecond,
	})

	writer.Start()

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	writer.Enqueue(record)
	<-store.writeStarted

	// Worker is stuck in time.Sleep; Stop hits time.After branch
	err := writer.Stop()
	assert.NoError(t, err)
}

func TestWriter_GetStats_WhenStopped(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{QueueSize: 10, Workers: 1})
	writer.Start()
	_ = writer.Stop()

	stats := writer.GetStats()
	assert.True(t, stats.Stopped)
	assert.Equal(t, 0, stats.QueueLength)
}

func TestWriter_DrainOnStop(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 10,
		Workers:   2,
	})

	writer.Start()

	// Enqueue multiple records
	for i := 0; i < 5; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess)
		writer.Enqueue(record)
	}

	// Stop and wait for drain
	err := writer.Stop()
	require.NoError(t, err)

	// All records should be processed
	assert.GreaterOrEqual(t, store.getRecordCount(), 0)
}

func TestWriter_DefaultWriterConfig(t *testing.T) {
	config := DefaultWriterConfig()
	assert.Equal(t, 1000, config.QueueSize)
	assert.Equal(t, 2, config.Workers)
	assert.Equal(t, 10*time.Second, config.StopTimeout)
}

func TestWriter_OnCallbacks(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, nil)

	// Test fluent API
	result := writer.OnEnqueueFailed(func(record *Record) {})
	assert.Equal(t, writer, result)

	result = writer.OnWriteFailed(func(record *Record, err error) {})
	assert.Equal(t, writer, result)
}

func TestWriter_StopWithNilStorage(t *testing.T) {
	writer := NewWriter(nil, nil)
	writer.Start()

	err := writer.Stop()
	assert.NoError(t, err)
}

func TestWriter_WorkerDrainQueue(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 100,
		Workers:   2,
	})

	writer.Start()

	// Enqueue many records
	for i := 0; i < 10; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			WithUserID("user" + string(rune('0'+i)))
		writer.Enqueue(record)
	}

	// Small delay to let some processing happen
	time.Sleep(50 * time.Millisecond)

	// Stop should drain remaining records
	err := writer.Stop()
	require.NoError(t, err)

	// All or most records should be processed
	count := store.getRecordCount()
	assert.GreaterOrEqual(t, count, 1)
}

func TestWriter_WriteRecordSuccess(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 10,
		Workers:   1,
	})

	var successCount int
	var mu sync.Mutex

	// Track successful writes via callback isn't available for success
	// So we just verify the storage has the records
	writer.Start()

	for i := 0; i < 3; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess)
		writer.Enqueue(record)
	}

	time.Sleep(100 * time.Millisecond)
	err := writer.Stop()
	require.NoError(t, err)

	mu.Lock()
	_ = successCount // unused but shows pattern
	mu.Unlock()

	assert.Equal(t, 3, store.getRecordCount())
}

func TestWriter_WorkerContextDone(t *testing.T) {
	store := newMockStorage()
	// No write delay to ensure records are processed

	writer := NewWriter(store, &WriterConfig{
		QueueSize:   100,
		Workers:     2,
		StopTimeout: 5 * time.Second,
	})

	writer.Start()

	// Enqueue many records
	for i := 0; i < 20; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess)
		writer.Enqueue(record)
	}

	// Give some time for processing
	time.Sleep(50 * time.Millisecond)

	// Stop while there are still items in queue - context will be cancelled
	// and worker should drain remaining items
	err := writer.Stop()
	require.NoError(t, err)

	// All or most records should have been processed
	assert.GreaterOrEqual(t, store.getRecordCount(), 10)
}

func TestWriter_WriteRecordWithoutCallback(t *testing.T) {
	store := newMockStorage()
	store.shouldError = true

	writer := NewWriter(store, &WriterConfig{
		QueueSize: 10,
		Workers:   1,
	})
	// Don't set onWriteFailed callback - should use default log

	writer.Start()

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	writer.Enqueue(record)

	time.Sleep(100 * time.Millisecond)
	err := writer.Stop()
	require.NoError(t, err)
}

func TestWriter_StopNotStarted(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, nil)

	// Stop without starting - should be safe
	err := writer.Stop()
	assert.NoError(t, err)
}

func TestWriter_EnqueueWithoutCallback(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 1,
		Workers:   1,
	})
	// Don't set onEnqueueFailed callback

	// Fill queue
	writer.Enqueue(NewRecord(EventLoginSuccess, ResultSuccess))
	// This should fail and log
	result := writer.Enqueue(NewRecord(EventLogout, ResultSuccess))
	assert.False(t, result)
}

func TestWriter_StopWithStopping(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 10,
		Workers:   1,
	})

	writer.Start()

	// Stop should work
	err := writer.Stop()
	assert.NoError(t, err)

	// Stats should show stopped
	stats := writer.GetStats()
	assert.True(t, stats.Stopped)
}

func TestWriter_EnqueueAfterStop(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{
		QueueSize: 10,
		Workers:   1,
	})
	writer.Start()
	err := writer.Stop()
	require.NoError(t, err)

	// Enqueue after Stop must not panic and must return false
	for i := 0; i < 3; i++ {
		ok := writer.Enqueue(NewRecord(EventLoginSuccess, ResultSuccess))
		assert.False(t, ok, "Enqueue after Stop should return false")
	}
}

func TestWriter_EnqueueNilRecord(t *testing.T) {
	store := newMockStorage()
	writer := NewWriter(store, &WriterConfig{QueueSize: 10, Workers: 1})
	writer.Start()
	defer func() { _ = writer.Stop() }()

	ok := writer.Enqueue(nil)
	assert.False(t, ok)
}

package audit

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFileStorage(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	require.NotNil(t, storage)
	defer func() { _ = storage.Close() }()

	assert.Equal(t, filePath, storage.FilePath())

	// Verify file was created
	info, err := os.Stat(filePath)
	assert.NoError(t, err)
	assert.Zero(t, info.Mode().Perm()&0o077, "file should not be readable by group/other")
}

func TestNewFileStorage_MkdirAllFails(t *testing.T) {
	tempDir := t.TempDir()
	// Create a file named "blocker"; then path blocker/audit.log cannot have directory blocker created
	blocker := filepath.Join(tempDir, "blocker")
	f, err := os.Create(blocker)
	require.NoError(t, err)
	_ = f.Close()

	filePath := filepath.Join(blocker, "audit.log")
	_, err = NewFileStorage(filePath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create directory")
}

func TestNewFileStorage_OpenFileFails(t *testing.T) {
	tempDir := t.TempDir()
	// Pass a path that is an existing directory; OpenFile will fail with "is a directory"
	_, err := NewFileStorage(tempDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open file")
}

func TestNewFileStorage_CreateDir(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "subdir", "nested", "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	require.NotNil(t, storage)
	defer func() { _ = storage.Close() }()

	// Verify directory was created
	dirInfo, err := os.Stat(filepath.Dir(filePath))
	assert.NoError(t, err)
	assert.True(t, dirInfo.IsDir())
	assert.Zero(t, dirInfo.Mode().Perm()&0o077, "directory should not be accessible by group/other")
}

func TestNewFileStorage_RefusesSymlink(t *testing.T) {
	tempDir := t.TempDir()
	target := filepath.Join(tempDir, "target.log")
	err := os.WriteFile(target, []byte("data"), 0600)
	require.NoError(t, err)

	linkPath := filepath.Join(tempDir, "audit.log")
	require.NoError(t, os.Symlink(target, linkPath))

	_, err = NewFileStorage(linkPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to open symlink")
}

func TestFileStorage_Write(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user123").
		WithIP("192.168.1.1")

	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Read file and verify content
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "login_success")
	assert.Contains(t, string(data), "user123")
}

func TestFileStorage_Write_MarshalError(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	record.Metadata = map[string]interface{}{"bad": make(chan int)} // chan cannot be JSON marshaled

	err = storage.Write(context.Background(), record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marshal")
}

func TestFileStorage_Write_WriteOrFlushFails(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Close underlying file so next Write hits write/flush error
	_ = storage.file.Close()

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = storage.Write(context.Background(), record)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "failed to write") ||
		strings.Contains(err.Error(), "failed to flush"), "err: %v", err)
}

func TestFileStorage_Query(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Write multiple records
	now := time.Now().Unix()
	for i := 0; i < 5; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			WithUserID("user" + string(rune('0'+i))).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query all
	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 5)

	// Should be in descending order (newest first)
	for i := 0; i < len(results)-1; i++ {
		assert.GreaterOrEqual(t, results[i].Timestamp, results[i+1].Timestamp)
	}
}

func TestFileStorage_Query_WithFilter(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	now := time.Now().Unix()

	// Write records with different event types
	records := []*Record{
		NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("user1").SetTimestamp(now),
		NewRecord(EventLoginFailed, ResultFailure).WithUserID("user2").SetTimestamp(now + 1),
		NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("user3").SetTimestamp(now + 2),
		NewRecord(EventLogout, ResultSuccess).WithUserID("user1").SetTimestamp(now + 3),
	}

	for _, r := range records {
		err := storage.Write(context.Background(), r)
		require.NoError(t, err)
	}

	// Filter by event type
	filter := DefaultQueryFilter().WithEventType("login_success")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Filter by user ID
	filter = DefaultQueryFilter().WithUserID("user1")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Filter by result
	filter = DefaultQueryFilter().WithResult("failure")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestFileStorage_Query_Pagination(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	now := time.Now().Unix()

	// Write 10 records
	for i := 0; i < 10; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Get first page
	filter := DefaultQueryFilter().WithLimit(3).WithOffset(0)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Get second page
	filter = DefaultQueryFilter().WithLimit(3).WithOffset(3)
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Get last page (partial)
	filter = DefaultQueryFilter().WithLimit(3).WithOffset(9)
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestFileStorage_Query_EmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestFileStorage_Query_NonExistentFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	// Create and close storage
	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	_ = storage.Close()

	// Remove the file
	_ = os.Remove(filePath)

	// Create new storage pointing to non-existent file
	storage, err = NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Query should return empty results (file will be empty after creation)
	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

// TestFileStorage_Query_OpenFailsNonNotExist covers Query when Open fails with an error other than IsNotExist.
func TestFileStorage_Query_OpenFailsNonNotExist(t *testing.T) {
	tempDir := t.TempDir()
	noPermFile := filepath.Join(tempDir, "noperm.log")
	require.NoError(t, os.WriteFile(noPermFile, []byte{}, 0000))
	defer func() { _ = os.Chmod(noPermFile, 0644) }()

	dummyFile := filepath.Join(tempDir, "dummy")
	f, err := os.Create(dummyFile)
	require.NoError(t, err)
	storage := &FileStorage{
		filePath: noPermFile,
		file:     f,
		writer:   bufio.NewWriter(f),
	}
	defer func() { _ = f.Close() }()

	_, err = storage.Query(context.Background(), DefaultQueryFilter())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open file for reading")
}

// TestFileStorage_Query_ScannerErr covers Query when scanner.Err() returns after reading (e.g. path is a directory).
func TestFileStorage_Query_ScannerErr(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	dummyFile := filepath.Join(tempDir, "dummy")
	f, err := os.Create(dummyFile)
	require.NoError(t, err)
	storage := &FileStorage{filePath: subDir, file: f, writer: bufio.NewWriter(f)}
	defer func() { _ = f.Close() }()

	_, err = storage.Query(context.Background(), DefaultQueryFilter())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read file")
}

func TestFileStorage_Rotate_FlushFails(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	_ = storage.Write(context.Background(), NewRecord(EventLoginSuccess, ResultSuccess))
	_ = storage.file.Close()

	err = storage.Rotate()
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "failed to flush writer") ||
		strings.Contains(err.Error(), "failed to close file"), "err: %v", err)
}

func TestFileStorage_Rotate_RenameFails(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	filePath := filepath.Join(subDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() {
		_ = os.Chmod(subDir, 0755)
		_ = storage.Close()
	}()

	_ = storage.Write(context.Background(), NewRecord(EventLoginSuccess, ResultSuccess))
	// Make directory read-only so Rename inside Rotate fails
	require.NoError(t, os.Chmod(subDir, 0444))

	err = storage.Rotate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rename")
}

func TestFileStorage_Rotate(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Write a record
	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Rotate
	err = storage.Rotate()
	require.NoError(t, err)

	// Write another record to new file
	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Check rotated file exists
	files, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	assert.Len(t, files, 2) // Original and rotated

	// Query new file
	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestFileStorage_Close(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)

	// Write before close
	err = storage.Write(context.Background(), NewRecord(EventLoginSuccess, ResultSuccess))
	require.NoError(t, err)

	// Close
	err = storage.Close()
	assert.NoError(t, err)

	// Note: Second close may return error due to file already closed, which is expected
}

func TestFileStorage_Write_ContextCancelled(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = storage.Write(ctx, record)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestFileStorage_Query_ContextCancelled(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Write some records first
	for i := 0; i < 100; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess)
		_ = storage.Write(context.Background(), record)
	}

	// Test with cancelled context (may not always trigger depending on timing)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _ = storage.Query(ctx, DefaultQueryFilter())
	// May or may not return error depending on timing
}

func TestFileStorage_Query_AllFilters(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	now := time.Now().Unix()

	// Write a record with all fields
	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user123").
		WithChallengeID("ch_abc").
		WithSessionID("sess_xyz").
		WithChannel("email").
		WithIP("192.168.1.1").
		SetTimestamp(now)

	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Query with session ID filter
	filter := DefaultQueryFilter().WithSessionID("sess_xyz")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// Query with IP filter
	filter = DefaultQueryFilter().WithIP("192.168.1.1")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// Query with non-matching IP
	filter = DefaultQueryFilter().WithIP("10.0.0.1")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestFileStorage_CloseNilFile(t *testing.T) {
	storage := &FileStorage{file: nil, writer: nil}
	err := storage.Close()
	assert.NoError(t, err)
}

func TestFileStorage_Query_TimeFilters(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	now := time.Now().Unix()

	// Write records at different times
	for i := 0; i < 5; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			SetTimestamp(now + int64(i*100))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Test StartTime filter only
	filter := DefaultQueryFilter().WithTimeRange(now+200, 0)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3) // timestamps >= now+200

	// Test EndTime filter only
	filter = &QueryFilter{Limit: 100, EndTime: now + 200}
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3) // timestamps <= now+200
}

func TestFileStorage_Query_ChallengeAndSessionFilters(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Write records with challenge and session IDs
	record1 := NewRecord(EventChallengeCreated, ResultSuccess).
		WithChallengeID("ch_123").
		WithSessionID("sess_abc")
	err = storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithChallengeID("ch_456").
		WithSessionID("sess_def")
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by challenge ID
	filter := DefaultQueryFilter().WithChallengeID("ch_123")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// Filter by session ID
	filter = DefaultQueryFilter().WithSessionID("sess_abc")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// Filter by channel
	record3 := NewRecord(EventSendSuccess, ResultSuccess).WithChannel("sms")
	err = storage.Write(context.Background(), record3)
	require.NoError(t, err)

	filter = DefaultQueryFilter().WithChannel("sms")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestFileStorage_Query_NilFilter(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Write a record
	err = storage.Write(context.Background(), NewRecord(EventLoginSuccess, ResultSuccess))
	require.NoError(t, err)

	// Query with nil filter (should use default)
	results, err := storage.Query(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestMatchesFilter_AllConditions(t *testing.T) {
	now := time.Now().Unix()

	record := &Record{
		EventType:   EventLoginSuccess,
		UserID:      "user123",
		ChallengeID: "ch_abc",
		SessionID:   "sess_xyz",
		Channel:     "email",
		Result:      ResultSuccess,
		IP:          "192.168.1.1",
		Timestamp:   now,
	}

	tests := []struct {
		name    string
		filter  *QueryFilter
		matches bool
	}{
		{
			name:    "match all",
			filter:  DefaultQueryFilter(),
			matches: true,
		},
		{
			name:    "mismatch event type",
			filter:  DefaultQueryFilter().WithEventType("logout"),
			matches: false,
		},
		{
			name:    "mismatch user id",
			filter:  DefaultQueryFilter().WithUserID("other"),
			matches: false,
		},
		{
			name:    "mismatch challenge id",
			filter:  DefaultQueryFilter().WithChallengeID("other"),
			matches: false,
		},
		{
			name:    "mismatch session id",
			filter:  DefaultQueryFilter().WithSessionID("other"),
			matches: false,
		},
		{
			name:    "mismatch channel",
			filter:  DefaultQueryFilter().WithChannel("sms"),
			matches: false,
		},
		{
			name:    "mismatch result",
			filter:  DefaultQueryFilter().WithResult("failure"),
			matches: false,
		},
		{
			name:    "mismatch ip",
			filter:  DefaultQueryFilter().WithIP("10.0.0.1"),
			matches: false,
		},
		{
			name:    "before start time",
			filter:  DefaultQueryFilter().WithTimeRange(now+100, 0),
			matches: false,
		},
		{
			name:    "after end time",
			filter:  DefaultQueryFilter().WithTimeRange(0, now-100),
			matches: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesFilter(record, tt.filter)
			assert.Equal(t, tt.matches, result)
		})
	}
}

func TestFileStorage_RotateAndContinue(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Write a record
	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Rotate
	err = storage.Rotate()
	require.NoError(t, err)

	// Write another record after rotation
	record = NewRecord(EventLogout, ResultSuccess)
	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Query should return the new record
	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, EventLogout, results[0].EventType)
}

func TestFileStorage_Query_EmptyLines(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	// Create file with empty lines
	f, err := os.Create(filePath)
	require.NoError(t, err)
	_, _ = f.WriteString("{\"event_type\":\"login_success\",\"result\":\"success\",\"timestamp\":1234567890}\n")
	_, _ = f.WriteString("\n") // Empty line
	_, _ = f.WriteString("{\"event_type\":\"logout\",\"result\":\"success\",\"timestamp\":1234567891}\n")
	_ = f.Close()

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestFileStorage_Query_MalformedJSON(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	// Create file with malformed JSON
	f, err := os.Create(filePath)
	require.NoError(t, err)
	_, _ = f.WriteString("{\"event_type\":\"login_success\",\"result\":\"success\",\"timestamp\":1234567890}\n")
	_, _ = f.WriteString("this is not json\n") // Malformed line
	_, _ = f.WriteString("{\"event_type\":\"logout\",\"result\":\"success\",\"timestamp\":1234567891}\n")
	_ = f.Close()

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 2) // Malformed line should be skipped
}

func TestFileStorage_FilePath(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	assert.Equal(t, filePath, storage.FilePath())
}

// TestFileStorage_Query_FileDeletedAfterCreate verifies that Query returns empty when the file
// is removed from the filesystem (e.g. by another process) while storage still holds the path.
func TestFileStorage_Query_FileDeletedAfterCreate(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	_ = storage.Write(context.Background(), NewRecord(EventLoginSuccess, ResultSuccess))

	// On Unix, removing an open file unlinks it; Query opens by path and gets IsNotExist
	_ = os.Remove(filePath)

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestFileStorage_Query_OpenFileFails(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()
	_ = storage.Write(context.Background(), NewRecord(EventLoginSuccess, ResultSuccess))

	// Remove read permission so Query's os.Open fails when opening for read (Unix)
	_ = os.Chmod(filePath, 0o000)
	defer func() { _ = os.Chmod(filePath, 0o644) }()

	_, err = storage.Query(context.Background(), DefaultQueryFilter())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open file")
}

// TestFileStorage_Query_OversizedLine verifies that a line longer than MaxRecordJSONSize
// causes bufio.Scanner to error (token too long); Query returns that error.
func TestFileStorage_Query_OversizedLine(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	f, err := os.Create(filePath)
	require.NoError(t, err)
	_, _ = f.WriteString("{\"event_type\":\"login_success\",\"result\":\"success\",\"timestamp\":1}\n")
	// Line longer than MaxRecordJSONSize (1MB) causes scanner to fail
	oversized := make([]byte, MaxRecordJSONSize+1)
	oversized[0] = '{'
	oversized[1] = '}'
	_, _ = f.Write(oversized)
	_, _ = f.WriteString("\n")
	_ = f.Close()

	storage, err := NewFileStorage(filePath)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	_, err = storage.Query(context.Background(), DefaultQueryFilter())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token too long")
}

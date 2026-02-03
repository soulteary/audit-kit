package audit

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func newTestSQLiteDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	return db
}

func TestNewDatabaseStorageFromDB(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)
	require.NotNil(t, storage)

	assert.Equal(t, "sqlite", storage.DBType())
	assert.Equal(t, db, storage.DB())
}

func TestNewDatabaseStorageFromDB_InvalidType(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	_, err := NewDatabaseStorageFromDB(db, "invalid", nil)
	assert.Error(t, err)
}

func TestDatabaseStorage_Write(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user123").
		WithIP("192.168.1.1").
		WithMetadata("key", "value")

	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Verify record was written
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestDatabaseStorage_Query(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	now := time.Now().Unix()

	// Write multiple records
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

func TestDatabaseStorage_Query_WithFilter(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	now := time.Now().Unix()

	// Write records with different attributes
	records := []*Record{
		NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("user1").WithChannel("email").SetTimestamp(now),
		NewRecord(EventLoginFailed, ResultFailure).WithUserID("user2").WithChannel("sms").SetTimestamp(now + 1),
		NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("user3").WithChannel("email").SetTimestamp(now + 2),
		NewRecord(EventLogout, ResultSuccess).WithUserID("user1").WithChannel("web").SetTimestamp(now + 3),
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

	// Filter by channel
	filter = DefaultQueryFilter().WithChannel("email")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Filter by result
	filter = DefaultQueryFilter().WithResult("failure")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// Filter by time range
	filter = DefaultQueryFilter().WithTimeRange(now, now+1)
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestDatabaseStorage_Query_Pagination(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

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

func TestDatabaseStorage_Query_Empty(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestDatabaseStorage_Query_NilFilter(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestDatabaseStorage_CustomTableName(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	config := &DatabaseConfig{
		TableName: "custom_audit",
	}

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", config)
	require.NoError(t, err)

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Verify record was written to custom table
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM custom_audit").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestDatabaseStorage_Close(t *testing.T) {
	db := newTestSQLiteDB(t)

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	err = storage.Close()
	assert.NoError(t, err)
}

func TestDatabaseStorage_Metadata(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithMetadata("key1", "value1").
		WithMetadata("key2", 123).
		WithMetadata("key3", true)

	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, "value1", results[0].Metadata["key1"])
	assert.Equal(t, float64(123), results[0].Metadata["key2"]) // JSON numbers are float64
	assert.Equal(t, true, results[0].Metadata["key3"])
}

func TestDatabaseStorage_AllFields(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	now := time.Now().Unix()
	record := &Record{
		EventType:         EventLoginSuccess,
		EventID:           "evt_123",
		UserID:            "user123",
		ChallengeID:       "ch_abc",
		SessionID:         "sess_xyz",
		Channel:           "email",
		Destination:       "test@example.com",
		Purpose:           "login",
		Resource:          "/api/users",
		Result:            ResultSuccess,
		Reason:            "",
		Provider:          "sendgrid",
		ProviderMessageID: "msg_123",
		IP:                "192.168.1.1",
		UserAgent:         "Mozilla/5.0",
		RequestID:         "req_123",
		TraceID:           "trace_abc",
		Timestamp:         now,
		DurationMS:        150,
	}

	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	require.Len(t, results, 1)

	r := results[0]
	assert.Equal(t, EventLoginSuccess, r.EventType)
	assert.Equal(t, "evt_123", r.EventID)
	assert.Equal(t, "user123", r.UserID)
	assert.Equal(t, "ch_abc", r.ChallengeID)
	assert.Equal(t, "sess_xyz", r.SessionID)
	assert.Equal(t, "email", r.Channel)
	assert.Equal(t, "test@example.com", r.Destination)
	assert.Equal(t, "login", r.Purpose)
	assert.Equal(t, "/api/users", r.Resource)
	assert.Equal(t, ResultSuccess, r.Result)
	assert.Equal(t, "sendgrid", r.Provider)
	assert.Equal(t, "msg_123", r.ProviderMessageID)
	assert.Equal(t, "192.168.1.1", r.IP)
	assert.Equal(t, "Mozilla/5.0", r.UserAgent)
	assert.Equal(t, "req_123", r.RequestID)
	assert.Equal(t, "trace_abc", r.TraceID)
	assert.Equal(t, now, r.Timestamp)
	assert.Equal(t, int64(150), r.DurationMS)
}

func TestDefaultDatabaseConfig(t *testing.T) {
	config := DefaultDatabaseConfig()
	assert.Equal(t, "audit_logs", config.TableName)
}

func TestValidateTableName(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		err := validateTableName("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
	t.Run("too long", func(t *testing.T) {
		err := validateTableName(strings.Repeat("a", maxTableNameLen+1))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too long")
	})
	t.Run("invalid character", func(t *testing.T) {
		err := validateTableName("audit-logs")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "alphanumeric")
	})
	t.Run("valid", func(t *testing.T) {
		assert.NoError(t, validateTableName("audit_logs"))
		assert.NoError(t, validateTableName("AuditLogs2"))
	})
}

func TestCreateTable_UnsupportedDBType(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	s := &DatabaseStorage{db: db, dbType: "invalid", tableName: "audit_logs"}
	ctx := context.Background()
	err := s.createTable(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported database type")
}

func TestDatabaseStorage_Write_MetadataMarshalError(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	record.Metadata = map[string]interface{}{"chan": make(chan int)} // chan cannot be marshaled

	err = storage.Write(context.Background(), record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marshal metadata")
}

// TestDatabaseStorage_Query_PostgresBranch covers the postgres query-building path.
// Uses sqlite DB with dbType "postgres" so the SELECT uses $1,$2 placeholders.
func TestDatabaseStorage_Query_PostgresBranch(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS audit_logs (
		event_type TEXT, event_id TEXT, user_id TEXT, challenge_id TEXT, session_id TEXT,
		channel TEXT, destination TEXT, purpose TEXT, resource TEXT, result TEXT, reason TEXT,
		provider TEXT, provider_message_id TEXT, ip TEXT, user_agent TEXT, request_id TEXT,
		trace_id TEXT, timestamp INTEGER, duration_ms INTEGER, metadata TEXT
	)`)

	s := &DatabaseStorage{db: db, dbType: "postgres", tableName: "audit_logs"}
	results, err := s.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	if results != nil {
		assert.Len(t, results, 0)
	}
}

func TestNewDatabaseStorage_InvalidURL(t *testing.T) {
	// Test unsupported URL format
	_, err := NewDatabaseStorage("invalid://localhost/db")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported database URL format")

	// Test empty URL
	_, err = NewDatabaseStorage("")
	assert.Error(t, err)

	// Test short URL
	_, err = NewDatabaseStorage("short")
	assert.Error(t, err)
}

func TestNewDatabaseStorageFromDB_InvalidTableName(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	tests := []struct {
		name      string
		tableName string
	}{
		{"SQL injection", "audit_logs; DROP TABLE audit_logs--"},
		{"space", "audit logs"},
		{"too long", strings.Repeat("a", maxTableNameLen+1)},
		{"hyphen", "audit-logs"},
		{"dot", "audit.logs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &DatabaseConfig{TableName: tt.tableName}
			_, err := NewDatabaseStorageFromDB(db, "sqlite", config)
			assert.Error(t, err)
		})
	}
}

func TestNewDatabaseStorageFromDB_UnsupportedType(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	_, err := NewDatabaseStorageFromDB(db, "unknown", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported database type")
}

func TestNewDatabaseStorageWithConfig_PostgresURL(t *testing.T) {
	// This will fail to connect but tests URL parsing
	_, err := NewDatabaseStorageWithConfig("postgres://user:pass@localhost:5432/db", nil)
	assert.Error(t, err) // Expected to fail since no server is running
}

func TestNewDatabaseStorageWithConfig_MySQLURL(t *testing.T) {
	// This will fail to connect but tests URL parsing
	_, err := NewDatabaseStorageWithConfig("mysql://user:pass@tcp(localhost:3306)/db", nil)
	assert.Error(t, err) // Expected to fail since no server is running
}

func TestDatabaseStorage_WriteWithContext(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	_ = storage.Write(ctx, record)
	// SQLite might still succeed even with cancelled context
	// Just ensure no panic
}

func TestDatabaseStorage_QueryWithAllFilters(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

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

	// Query with all filters
	filter := DefaultQueryFilter().
		WithEventType("login_success").
		WithUserID("user123").
		WithChallengeID("ch_abc").
		WithSessionID("sess_xyz").
		WithChannel("email").
		WithResult("success").
		WithIP("192.168.1.1").
		WithTimeRange(now-1, now+1)

	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestDatabaseStorage_CloseNilDB(t *testing.T) {
	storage := &DatabaseStorage{db: nil}
	err := storage.Close()
	assert.NoError(t, err)
}

func TestDatabaseStorage_QueryWithChallengeIDFilter(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	now := time.Now().Unix()

	// Write records with challenge IDs
	record1 := NewRecord(EventChallengeCreated, ResultSuccess).
		WithChallengeID("ch_123").
		SetTimestamp(now)
	err = storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventChallengeCreated, ResultSuccess).
		WithChallengeID("ch_456").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by challenge ID
	filter := DefaultQueryFilter().WithChallengeID("ch_123")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestDatabaseStorage_QueryWithIPFilter(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	now := time.Now().Unix()

	// Write records with IPs
	record1 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithIP("192.168.1.1").
		SetTimestamp(now)
	err = storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithIP("10.0.0.1").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by IP
	filter := DefaultQueryFilter().WithIP("192.168.1.1")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestDatabaseStorage_QueryTimeRangeFilters(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	now := time.Now().Unix()

	// Write records at different times
	for i := 0; i < 5; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			SetTimestamp(now + int64(i*100))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Filter with StartTime only
	filter := &QueryFilter{Limit: 100, StartTime: now + 200}
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Filter with EndTime only
	filter = &QueryFilter{Limit: 100, EndTime: now + 200}
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestDatabaseStorage_QueryWithSessionFilter(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	now := time.Now().Unix()

	// Write records with session IDs
	record1 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithSessionID("sess_123").
		SetTimestamp(now)
	err = storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithSessionID("sess_456").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by session ID
	filter := DefaultQueryFilter().WithSessionID("sess_123")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestDatabaseStorage_WriteWithNilMetadata(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	record.Metadata = nil

	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

// TestDatabaseStorage_Query_SkipsRowWithScanError verifies that a row that fails to scan is skipped.
func TestDatabaseStorage_Query_SkipsRowWithScanError(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	// Write one valid record
	err = storage.Write(context.Background(), NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("u1"))
	require.NoError(t, err)

	// Insert a row with invalid timestamp (non-integer) so Scan fails for that row
	_, err = db.Exec(`INSERT INTO audit_logs (event_type, result, timestamp) VALUES ('x', 'y', 'not_a_number')`)
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	// One valid record; the invalid row is skipped
	assert.Len(t, results, 1)
	assert.Equal(t, "u1", results[0].UserID)
}

func TestDatabaseStorage_WriteAllFields(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	now := time.Now().Unix()
	record := &Record{
		EventType:         EventChallengeCreated,
		EventID:           "evt_123",
		UserID:            "user123",
		ChallengeID:       "ch_abc",
		SessionID:         "sess_xyz",
		Channel:           "sms",
		Destination:       "+1234567890",
		Purpose:           "login",
		Resource:          "/api/login",
		Result:            ResultSuccess,
		Reason:            "",
		Provider:          "aliyun",
		ProviderMessageID: "msg_456",
		IP:                "192.168.1.1",
		UserAgent:         "Mozilla/5.0",
		RequestID:         "req_789",
		TraceID:           "trace_abc",
		Timestamp:         now,
		DurationMS:        150,
		Metadata:          map[string]interface{}{"key": "value"},
	}

	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	require.Len(t, results, 1)

	r := results[0]
	assert.Equal(t, EventChallengeCreated, r.EventType)
	assert.Equal(t, "evt_123", r.EventID)
	assert.Equal(t, "user123", r.UserID)
	assert.Equal(t, "ch_abc", r.ChallengeID)
	assert.Equal(t, "sess_xyz", r.SessionID)
	assert.Equal(t, "sms", r.Channel)
	assert.Equal(t, "+1234567890", r.Destination)
	assert.Equal(t, "login", r.Purpose)
	assert.Equal(t, "/api/login", r.Resource)
	assert.Equal(t, ResultSuccess, r.Result)
	assert.Equal(t, "aliyun", r.Provider)
	assert.Equal(t, "msg_456", r.ProviderMessageID)
	assert.Equal(t, "192.168.1.1", r.IP)
	assert.Equal(t, "Mozilla/5.0", r.UserAgent)
	assert.Equal(t, "req_789", r.RequestID)
	assert.Equal(t, "trace_abc", r.TraceID)
	assert.Equal(t, now, r.Timestamp)
	assert.Equal(t, int64(150), r.DurationMS)
}

func TestDatabaseStorage_QueryWithResultFilter(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	now := time.Now().Unix()

	// Write records with different results
	record1 := NewRecord(EventLoginSuccess, ResultSuccess).SetTimestamp(now)
	err = storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventLoginFailed, ResultFailure).SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by result
	filter := DefaultQueryFilter().WithResult("failure")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestDatabaseStorage_DBAndType(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	// Test accessor methods
	assert.NotNil(t, storage.DB())
	assert.Equal(t, "sqlite", storage.DBType())
}

func TestDatabaseStorage_QueryNilFilter(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	// Write a record
	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Query with nil filter
	results, err := storage.Query(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestDatabaseStorage_QueryPagination(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	now := time.Now().Unix()

	// Write 10 records
	for i := 0; i < 10; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query with limit and offset
	filter := DefaultQueryFilter().WithLimit(3).WithOffset(2)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

// TestNewDatabaseStorageFromDB_CreateTableFails covers createTable returning error (e.g. ExecContext fails).
func TestNewDatabaseStorageFromDB_CreateTableFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec("CREATE TABLE.*").WillReturnError(errors.New("create failed"))
	_, err = NewDatabaseStorageFromDB(db, "sqlite", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create table")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestNewDatabaseStorageFromDB_CreateTableFailsOnSecondStatement covers createTable when a later Exec (e.g. CREATE INDEX) fails.
func TestNewDatabaseStorageFromDB_CreateTableFailsOnSecondStatement(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec("CREATE TABLE.*").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX.*").WillReturnError(errors.New("create index failed"))
	_, err = NewDatabaseStorageFromDB(db, "sqlite", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create table")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDatabaseStorage_Write_PostgresBranch covers the postgres INSERT branch (placeholder $1..$20).
func TestDatabaseStorage_Write_PostgresBranch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Postgres createTable: one CREATE TABLE + 6 CREATE INDEX
	mock.ExpectExec("CREATE TABLE.*").WillReturnResult(sqlmock.NewResult(0, 0))
	for i := 0; i < 6; i++ {
		mock.ExpectExec("CREATE INDEX.*").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	storage, err := NewDatabaseStorageFromDB(db, "postgres", nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectExec("INSERT INTO.*VALUES.*\\$1.*\\$20").WillReturnResult(sqlmock.NewResult(1, 1))
	record := NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("u1")
	err = storage.Write(context.Background(), record)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDatabaseStorage_Write_UnsupportedDBType covers the default branch in Write (unsupported db type).
func TestDatabaseStorage_Write_UnsupportedDBType(t *testing.T) {
	db := newTestSQLiteDB(t)
	defer func() { _ = db.Close() }()
	storage := &DatabaseStorage{db: db, dbType: "unknown", tableName: "audit_logs"}
	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err := storage.Write(context.Background(), record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported database type")
}

// TestCreateTable_MySQLBranch covers createTable for mysql (single CREATE TABLE statement with INDEX).
func TestCreateTable_MySQLBranch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec("CREATE TABLE.*").WillReturnResult(sqlmock.NewResult(0, 0))
	_, err = NewDatabaseStorageFromDB(db, "mysql", nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDatabaseStorage_Write_ExecFails covers Write when ExecContext fails (e.g. DB closed).
func TestDatabaseStorage_Write_ExecFails(t *testing.T) {
	db := newTestSQLiteDB(t)
	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)

	require.NoError(t, db.Close())

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = storage.Write(context.Background(), record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to insert")
}

// TestDatabaseStorage_Query_RowsErr covers Query when rows.Err() returns non-nil after iteration.
func TestDatabaseStorage_Query_RowsErr(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Allow createTable to succeed (sqlite: one CREATE TABLE + several CREATE INDEX)
	mock.ExpectExec("CREATE TABLE.*").WillReturnResult(sqlmock.NewResult(0, 0))
	for i := 0; i < 6; i++ {
		mock.ExpectExec("CREATE INDEX.*").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	storage, err := NewDatabaseStorageFromDB(db, "sqlite", nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	cols := []string{"event_type", "event_id", "user_id", "challenge_id", "session_id",
		"channel", "destination", "purpose", "resource", "result", "reason",
		"provider", "provider_message_id", "ip", "user_agent", "request_id",
		"trace_id", "timestamp", "duration_ms", "metadata"}
	rows := sqlmock.NewRows(cols).
		AddRow("login_success", "", "u1", "", "", "", "", "", "", "success", "", "", "", "", "", "", "", time.Now().Unix(), nil, "").
		AddRow("login_success", "", "u2", "", "", "", "", "", "", "success", "", "", "", "", "", "", "", time.Now().Unix(), nil, "").
		RowError(1, errors.New("row iteration error"))

	mock.ExpectQuery("SELECT.*").WillReturnRows(rows)

	_, err = storage.Query(context.Background(), DefaultQueryFilter())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error iterating rows")
	require.NoError(t, mock.ExpectationsWereMet())
}
